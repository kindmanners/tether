// Command pairing-e2e-test exercises the REAL production Client and
// Server together — unlike cmd/pairing-server-test, which used a
// hand-rolled duplicate client to test Server in isolation before Client
// existed. This is the actual code path both real binaries will use.
// Delete once cmd/tether and cmd/tether-agent are wired to do this for
// real.
package main

import (
	"fmt"
	"os"
	"time"

	"tether/internal/certs"
	"tether/internal/pairing"
	"tether/internal/trust"
)

const addr = "127.0.0.1:17421"
const testWindow = 5 * time.Second

func main() {
	agentIdentity, err := certs.LoadOrCreate("e2e-agent")
	if err != nil {
		fmt.Fprintf(os.Stderr, "FATAL: agent identity: %v\n", err)
		os.Exit(1)
	}
	orchestratorIdentity, err := certs.LoadOrCreate("e2e-orchestrator")
	if err != nil {
		fmt.Fprintf(os.Stderr, "FATAL: orchestrator identity: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("=== Test 1: real Client + real Server, successful pairing ===")
	func() {
		srv, err := pairing.NewServer(agentIdentity, testWindow)
		if err != nil {
			panic(err)
		}
		code := srv.Code()

		serverDone := make(chan error, 1)
		go func() { serverDone <- srv.Start(addr) }()
		time.Sleep(200 * time.Millisecond)

		client := pairing.NewClient(orchestratorIdentity)
		result, err := client.Pair(addr, code)
		if err != nil {
			fmt.Printf("  FAIL: Pair() returned an error: %v\n", err)
		} else {
			expectedHostname := agentIdentity.Certificate.Subject.CommonName
			if result.AgentHostname != expectedHostname {
				fmt.Printf("  FAIL: expected agent hostname %q, got %q\n", expectedHostname, result.AgentHostname)
			} else {
				fmt.Printf("  PASS: Pair() succeeded, resolved agent hostname %q\n", result.AgentHostname)
			}
		}

		serverErr := <-serverDone
		if serverErr != nil {
			fmt.Printf("  FAIL: server-side Start() reported an error after a successful pairing: %v\n", serverErr)
		} else {
			fmt.Println("  PASS: server-side Start() returned nil (success)")
		}

		// The real end-to-end proof: BOTH sides pinned the OTHER side's
		// cert. Client.Pair pins the agent's cert; Server.handlePair
		// pins the orchestrator's cert. After one successful exchange,
		// trust.Get should find both, from each respective machine's
		// perspective (both stores live in the same process here since
		// this is a single-machine test, but the calls themselves are
		// the real production code each real machine would run).
		agentHostname := agentIdentity.Certificate.Subject.CommonName
		orchestratorHostname := orchestratorIdentity.Certificate.Subject.CommonName

		if _, found, err := trust.Get(agentHostname); err != nil {
			fmt.Printf("  FAIL: trust.Get(agent) error: %v\n", err)
		} else if !found {
			fmt.Println("  FAIL: agent cert was not pinned by Client.Pair")
		} else {
			fmt.Println("  PASS: agent cert was pinned (by Client.Pair)")
		}

		if _, found, err := trust.Get(orchestratorHostname); err != nil {
			fmt.Printf("  FAIL: trust.Get(orchestrator) error: %v\n", err)
		} else if !found {
			fmt.Println("  FAIL: orchestrator cert was not pinned by Server.handlePair")
		} else {
			fmt.Println("  PASS: orchestrator cert was pinned (by Server.handlePair) — MUTUAL pinning confirmed")
		}
	}()

	fmt.Println()
	fmt.Println("=== Test 2: wrong code via real Client, then retry with correct code ===")
	func() {
		srv, err := pairing.NewServer(agentIdentity, testWindow)
		if err != nil {
			panic(err)
		}
		code := srv.Code()

		serverDone := make(chan error, 1)
		go func() { serverDone <- srv.Start(addr) }()
		time.Sleep(200 * time.Millisecond)

		client := pairing.NewClient(orchestratorIdentity)
		_, err = client.Pair(addr, "WRONGCODE")
		if err == nil {
			fmt.Println("  FAIL: Pair() succeeded with a wrong code")
		} else {
			fmt.Printf("  PASS: Pair() correctly failed with wrong code: %v\n", err)
		}

		result, err := client.Pair(addr, code)
		if err != nil {
			fmt.Printf("  FAIL: retry with correct code failed: %v (DoS vulnerability may be back)\n", err)
		} else {
			fmt.Printf("  PASS: retry with correct code succeeded via real Client — got %q\n", result.AgentHostname)
		}

		<-serverDone
	}()

	fmt.Println()
	fmt.Println("=== Test 3: Client.Pair against a server whose window already expired ===")
	func() {
		srv, err := pairing.NewServer(agentIdentity, 1*time.Second)
		if err != nil {
			panic(err)
		}
		serverDone := make(chan error, 1)
		go func() { serverDone <- srv.Start(addr) }()

		time.Sleep(2 * time.Second) // let the window fully close first

		client := pairing.NewClient(orchestratorIdentity)
		_, err = client.Pair(addr, srv.Code())
		if err == nil {
			fmt.Println("  FAIL: Pair() succeeded against a server whose window had already closed")
		} else {
			fmt.Printf("  PASS: Pair() correctly failed against a closed server: %v\n", err)
		}

		<-serverDone
	}()
}