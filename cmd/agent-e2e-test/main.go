// Command agent-e2e-test proves internal/agent's Server and Client work
// together correctly over REAL mutual TLS (built via
// trust.PinnedTLSConfig, using real pinned certs) commanding a REAL
// subprocess (via internal/process.Manager) — the full chain this
// project has been building toward: pairing establishes trust, trust
// secures commands, commands control a real process.
//
// Reuses the same self-re-invocation trick as cmd/process-test for a
// controllable stand-in "rpc-server" binary, and a temporary
// agentconfig.yaml pointing at it and a fake "model" file.
//
// Delete once cmd/tether and cmd/tether-agent are wired to do this for
// real.
package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"tether/internal/agent"
	"tether/internal/config"
	"tether/internal/certs"
	"tether/internal/process"
	"tether/internal/trust"
)

const addr = "127.0.0.1:17423"

const sleepHelperMarker = "__agent_e2e_sleep_helper__"

func main() {
	if len(os.Args) > 1 && os.Args[1] == "--model" && os.Args[2] == sleepHelperMarker {
		// os.Args here is: [self, "--model", sleepHelperMarker, "--port", "N"]
		var seconds int
		fmt.Sscanf(os.Args[4], "%d", &seconds)
		time.Sleep(time.Duration(seconds) * time.Second)
		os.Exit(0)
	}
	runTests()
}

func runTests() {
	self, err := os.Executable()
	if err != nil {
		panic(err)
	}

	// Build a real agentconfig pointing this test's own binary as the
	// "rpc-server", and the sentinel string as the one approved "model".
	// A model file needs to actually exist on disk for agentconfig's
	// validation to accept it — create a harmless empty temp file.
	tmpDir, err := os.MkdirTemp("", "agent-e2e-test-*")
	if err != nil {
		panic(err)
	}
	defer os.RemoveAll(tmpDir)

	fakeModelPath := filepath.Join(tmpDir, "fake-model.gguf")
	if err := os.WriteFile(fakeModelPath, []byte("not a real model"), 0644); err != nil {
		panic(err)
	}

	cfg := &agentconfig.Config{
		RPCServerPath: self,
		Models: []agentconfig.ModelEntry{
			{Name: "test-model", Path: fakeModelPath},
		},
	}
	// Bypassing agentconfig.Load (which reads from a file) since we're
	// constructing Config directly for this test — but we still need its
	// Resolve() to work, which only depends on the in-memory struct, not
	// on how it was loaded.

	agentIdentity, err := certs.LoadOrCreate("agent-e2e-agent")
	if err != nil {
		panic(err)
	}
	orchestratorIdentity, err := certs.LoadOrCreate("agent-e2e-orchestrator")
	if err != nil {
		panic(err)
	}
	if err := trust.Pin("agent-e2e-agent", agentIdentity.CertDER); err != nil {
		panic(err)
	}
	if err := trust.Pin("agent-e2e-orchestrator", orchestratorIdentity.CertDER); err != nil {
		panic(err)
	}

	agentTLS, err := trust.PinnedTLSConfig(agentIdentity.TLSCertificate(), "agent-e2e-orchestrator", true)
	if err != nil {
		panic(fmt.Sprintf("building agent TLS config: %v", err))
	}
	orchestratorTLS, err := trust.PinnedTLSConfig(orchestratorIdentity.TLSCertificate(), "agent-e2e-agent", false)
	if err != nil {
		panic(fmt.Sprintf("building orchestrator TLS config: %v", err))
	}

	manager := process.NewManager()
	server := agent.NewServer(cfg, manager)

	ctx, cancelServer := context.WithCancel(context.Background())
	serverErrCh := make(chan error, 1)
	go func() { serverErrCh <- server.Start(ctx, addr, agentTLS) }()
	time.Sleep(300 * time.Millisecond) // let the listener come up

	client := agent.NewClient(orchestratorTLS)

	fmt.Println("=== Test 1: status before anything has started ===")
	status, err := client.GetStatus(addr)
	if err != nil {
		fmt.Printf("  FAIL: GetStatus error: %v\n", err)
	} else if status.Status != "Stopped" {
		fmt.Printf("  FAIL: expected Stopped, got %s\n", status.Status)
	} else {
		fmt.Println("  PASS: status correctly reports Stopped")
	}

	fmt.Println()
	fmt.Println("=== Test 2: start the (stand-in) rpc-server via a real command, over real mTLS ===")
	// port value repurposed by our helper as sleep duration, per the
	// sentinel-model trick — this test isn't exercising real rpc-server
	// semantics, it's proving the Client->Server->Manager chain actually
	// launches and tracks a real process correctly end-to-end.
	status, err = client.StartRPCServer(addr, "test-model", 30)
	if err != nil {
		fmt.Printf("  FAIL: StartRPCServer error: %v\n", err)
	} else if status.Status != "Running" {
		fmt.Printf("  FAIL: expected Running immediately after start, got %s\n", status.Status)
	} else {
		fmt.Println("  PASS: start command succeeded, status reports Running")
	}

	fmt.Println()
	fmt.Println("=== Test 3: an unapproved model name is rejected — never reaches the process layer ===")
	_, err = client.StartRPCServer(addr, "not-an-approved-model", 9999)
	if err == nil {
		fmt.Println("  FAIL: unapproved model name was accepted")
	} else {
		fmt.Printf("  PASS: rejected as expected: %v\n", err)
	}

	fmt.Println()
	fmt.Println("=== Test 4: stop the running process via a real command ===")
	status, err = client.StopRPCServer(addr)
	if err != nil {
		fmt.Printf("  FAIL: StopRPCServer error: %v\n", err)
	} else if status.Status != "Stopped" {
		fmt.Printf("  FAIL: expected Stopped after stop, got %s\n", status.Status)
	} else {
		fmt.Println("  PASS: stop command succeeded, status reports Stopped")
	}

	fmt.Println()
	fmt.Println("=== Test 5: shut the command server down via context cancellation ===")
	cancelServer()
	select {
	case err := <-serverErrCh:
		if err != nil {
			fmt.Printf("  FAIL: Start() returned an error on shutdown: %v\n", err)
		} else {
			fmt.Println("  PASS: Start() returned cleanly after context cancellation")
		}
	case <-time.After(10 * time.Second):
		fmt.Println("  FAIL: server did not shut down within 10s")
	}
}
