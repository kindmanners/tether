// Command pairing-server-test is a throwaway harness that exercises
// internal/pairing.Server end-to-end over a REAL network connection —
// unlike cmd/pairing-test, which only tests Code's in-process logic. This
// is what actually proves the TLS setup, size limit, and DoS fix work
// over real HTTP, not just that the code compiles.
//
// Uses short windows (a few seconds, via NewServer's window parameter)
// rather than the real 2-minute pairing.Window, so this runs quickly.
// Delete once this code is wired into the real Agent/Orchestrator and
// exercised for real.
package main

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"tether/internal/certs"
	"tether/internal/pairing"
	"tether/internal/trust"
)

// pairRequestJSON and pairResponseJSON mirror the unexported types in
// internal/pairing/server.go — duplicated here deliberately, playing the
// role of what the real Orchestrator-side client will eventually be.
// This harness IS effectively a prototype of that client.
type pairRequestJSON struct {
	PairingCode         string `json:"pairing_code"`
	OrchestratorCertPEM string `json:"orchestrator_cert"`
}

type pairResponseJSON struct {
	AgentCertPEM string `json:"agent_cert"`
}

type pairErrorJSON struct {
	Error string `json:"error"`
}

// insecureClient is an HTTP client that skips TLS certificate
// verification — appropriate ONLY for this bootstrap pairing exchange,
// where the Orchestrator has no pinned cert for the Agent yet (see the
// doc comment on pairing.Server for the full reasoning). Every
// connection after a successful pairing must use a client that verifies
// against the cert trust.Pin stored, never this one.
var insecureClient = &http.Client{
	Transport: &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
	},
	Timeout: 5 * time.Second,
}

func attemptPair(addr, code, orchestratorCertPEM string) (int, *pairResponseJSON, *pairErrorJSON, error) {
	reqBody, err := json.Marshal(pairRequestJSON{
		PairingCode:         code,
		OrchestratorCertPEM: orchestratorCertPEM,
	})
	if err != nil {
		return 0, nil, nil, err
	}

	resp, err := insecureClient.Post("https://"+addr+"/pair", "application/json", bytes.NewReader(reqBody))
	if err != nil {
		return 0, nil, nil, err
	}
	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return resp.StatusCode, nil, nil, err
	}

	if resp.StatusCode == http.StatusOK {
		var ok pairResponseJSON
		if err := json.Unmarshal(bodyBytes, &ok); err != nil {
			return resp.StatusCode, nil, nil, fmt.Errorf("decoding success body: %w", err)
		}
		return resp.StatusCode, &ok, nil, nil
	}

	var errResp pairErrorJSON
	if err := json.Unmarshal(bodyBytes, &errResp); err != nil {
		return resp.StatusCode, nil, nil, fmt.Errorf("decoding error body: %w", err)
	}
	return resp.StatusCode, nil, &errResp, nil
}

func main() {
	agentIdentity, err := certs.LoadOrCreate("test-agent")
	if err != nil {
		fmt.Fprintf(os.Stderr, "FATAL: could not load agent identity: %v\n", err)
		os.Exit(1)
	}

	orchestratorIdentity, err := certs.LoadOrCreate("test-orchestrator")
	if err != nil {
		fmt.Fprintf(os.Stderr, "FATAL: could not load orchestrator identity: %v\n", err)
		os.Exit(1)
	}
	orchestratorCertPEM := string(pem.EncodeToMemory(&pem.Block{
		Type:  "CERTIFICATE",
		Bytes: orchestratorIdentity.CertDER,
	}))

	const addr = "127.0.0.1:17420"
	const testWindow = 5 * time.Second

	fmt.Println("=== Test 1: server serves real TLS, not plaintext ===")
	// Plaintext HTTP against a TLS-only listener should fail outright —
	// this proves ListenAndServeTLS is actually in effect, not silently
	// falling back to plain HTTP.
	func() {
		srv, err := pairing.NewServer(agentIdentity, testWindow)
		if err != nil {
			panic(err)
		}
		done := make(chan error, 1)
		go func() { done <- srv.Start(addr) }()
		time.Sleep(200 * time.Millisecond) // let the listener actually come up

		plainClient := &http.Client{Timeout: 2 * time.Second}
		resp, err := plainClient.Post("http://"+addr+"/pair", "application/json", bytes.NewReader([]byte("{}")))
		// A plaintext request to a TLS-only server should fail to
		// complete a valid HTTP exchange. Depending on exactly how Go's
		// TLS server closes the misdirected connection, this can surface
		// either as a client-side error OR as a response that never
		// actually carries a real HTTP status (Go's transport can return
		// without `err` set in some malformed-response cases) — so we
		// check both: either err is non-nil, or if a response object
		// came back at all, it must not be a normal 200. Checking err
		// alone was insufficient (an earlier version of this test did
		// exactly that and reported PASS-shaped output as FAIL, even
		// though the server logs showed it correctly rejecting the
		// plaintext request at the protocol level).
		if err != nil {
			fmt.Printf("  PASS: plaintext HTTP request correctly failed: %v\n", err)
		} else if resp != nil && resp.StatusCode == http.StatusOK {
			fmt.Println("  FAIL: plaintext HTTP request got a real 200 OK from a TLS-only server")
		} else {
			fmt.Println("  PASS: plaintext HTTP request did not complete normally (no error, but no valid 200 either — consistent with the TLS handshake rejection visible in the server's own log line above)")
		}
		if resp != nil {
			resp.Body.Close()
		}

		// Now make a correct-but-empty-code TLS request just to let the
		// server finish cleanly via window expiry rather than leaving it
		// hanging for the rest of this test run.
		<-done
	}()

	fmt.Println()
	fmt.Println("=== Test 2: full successful pairing exchange over real TLS ===")
	func() {
		srv, err := pairing.NewServer(agentIdentity, testWindow)
		if err != nil {
			panic(err)
		}
		code := srv.Code()
		done := make(chan error, 1)
		go func() { done <- srv.Start(addr) }()
		time.Sleep(200 * time.Millisecond)

		status, ok, errResp, err := attemptPair(addr, code, orchestratorCertPEM)
		if err != nil {
			fmt.Printf("  FAIL: request error: %v\n", err)
		} else if status != http.StatusOK {
			fmt.Printf("  FAIL: expected 200, got %d (%+v)\n", status, errResp)
		} else if ok.AgentCertPEM == "" {
			fmt.Println("  FAIL: got 200 but no agent cert in response")
		} else {
			block, _ := pem.Decode([]byte(ok.AgentCertPEM))
			if block == nil {
				fmt.Println("  FAIL: agent cert in response is not valid PEM")
			} else if _, err := x509.ParseCertificate(block.Bytes); err != nil {
				fmt.Printf("  FAIL: agent cert did not parse: %v\n", err)
			} else {
				fmt.Println("  PASS: received a valid, parseable agent certificate")
			}
		}

		startErr := <-done
		if startErr != nil {
			fmt.Printf("  FAIL: Start() returned an error after a successful pairing: %v\n", startErr)
		} else {
			fmt.Println("  PASS: Start() returned nil (success) after the exchange")
		}

		// Confirm the orchestrator's cert actually got pinned as a side
		// effect — this is the real end goal of pairing, not just an
		// HTTP 200.
		orchestratorHostname := orchestratorIdentity.Certificate.Subject.CommonName
		peer, found, err := trust.Get(orchestratorHostname)
		if err != nil {
			fmt.Printf("  FAIL: trust.Get after pairing returned an error: %v\n", err)
		} else if !found {
			fmt.Println("  FAIL: orchestrator cert was not pinned after a successful pairing")
		} else {
			fmt.Printf("  PASS: orchestrator cert for %q is now pinned (trusted at %s)\n", peer.Hostname, peer.TrustedAt.Format(time.RFC3339))
		}
	}()

	fmt.Println()
	fmt.Println("=== Test 3: wrong code does NOT kill the server; correct code afterward still works ===")
	func() {
		srv, err := pairing.NewServer(agentIdentity, testWindow)
		if err != nil {
			panic(err)
		}
		code := srv.Code()
		done := make(chan error, 1)
		go func() { done <- srv.Start(addr) }()
		time.Sleep(200 * time.Millisecond)

		status, _, errResp, err := attemptPair(addr, "WRONGCODE", orchestratorCertPEM)
		if err != nil {
			fmt.Printf("  FAIL: request error on wrong-code attempt: %v\n", err)
		} else if status != http.StatusUnauthorized {
			fmt.Printf("  FAIL: expected 401 for wrong code, got %d\n", status)
		} else {
			fmt.Printf("  PASS: wrong code correctly rejected with 401 (%q)\n", errResp.Error)
		}

		// The real proof: the CORRECT code, sent right after, over a
		// fresh connection, must still succeed. Before the fix, the
		// server would already be dead at this point.
		status2, ok2, errResp2, err := attemptPair(addr, code, orchestratorCertPEM)
		if err != nil {
			fmt.Printf("  FAIL: request error on correct-code follow-up: %v\n", err)
		} else if status2 != http.StatusOK {
			fmt.Printf("  FAIL: expected 200 on correct code after a prior wrong attempt, got %d (%+v) — DoS vulnerability is back\n", status2, errResp2)
		} else {
			fmt.Printf("  PASS: correct code succeeded after a prior wrong attempt over real HTTP — got agent cert (%d bytes)\n", len(ok2.AgentCertPEM))
		}

		<-done
	}()

	fmt.Println()
	fmt.Println("=== Test 4: oversized request body is rejected (413) ===")
	func() {
		srv, err := pairing.NewServer(agentIdentity, testWindow)
		if err != nil {
			panic(err)
		}
		done := make(chan error, 1)
		go func() { done <- srv.Start(addr) }()
		time.Sleep(200 * time.Millisecond)

		// A ~100KB body against a 64KB limit.
		hugeCert := make([]byte, 100*1024)
		for i := range hugeCert {
			hugeCert[i] = 'A'
		}
		reqBody, _ := json.Marshal(pairRequestJSON{
			PairingCode:         "WHATEVER",
			OrchestratorCertPEM: string(hugeCert),
		})
		resp, err := insecureClient.Post("https://"+addr+"/pair", "application/json", bytes.NewReader(reqBody))
		if err != nil {
			fmt.Printf("  FAIL: request error: %v\n", err)
		} else {
			resp.Body.Close()
			if resp.StatusCode == http.StatusRequestEntityTooLarge {
				fmt.Println("  PASS: oversized body correctly rejected with 413")
			} else {
				fmt.Printf("  FAIL: expected 413, got %d\n", resp.StatusCode)
			}
		}

		<-done
	}()

	fmt.Println()
	fmt.Println("=== Test 5: window expiry actually shuts the server down ===")
	func() {
		srv, err := pairing.NewServer(agentIdentity, 2*time.Second)
		if err != nil {
			panic(err)
		}
		start := time.Now()
		err = srv.Start(addr) // no requests sent at all — should time out on its own
		elapsed := time.Since(start)
		if err == nil {
			fmt.Println("  FAIL: Start() returned nil (success) with no request ever sent")
		} else if elapsed < 2*time.Second || elapsed > 4*time.Second {
			fmt.Printf("  FAIL: Start() returned after %s, expected ~2s\n", elapsed.Round(time.Millisecond))
		} else {
			fmt.Printf("  PASS: Start() correctly timed out after %s with no valid attempt: %v\n", elapsed.Round(time.Millisecond), err)
		}
	}()
}