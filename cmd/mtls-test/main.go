// Copyright (C) 2026 kindmanners on github
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as
// published by the Free Software Foundation, either version 3 of the
// License, or (at your option) any later version.
//
// This program is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
// GNU Affero General Public License for more details.
//
// You should have received a copy of the GNU Affero General Public License
// along with this program.  If not, see <https://www.gnu.org/licenses/>.

// Command mtls-test proves internal/trust.PinnedTLSConfig actually
// establishes a real mutual TLS connection when both sides' pins match,
// and actually REJECTS one when they don't — over a real TCP connection,
// not a mock. Written specifically because an earlier version of
// PinnedTLSConfig had a subtle, wrong claim about how ClientAuth
// interacts with InsecureSkipVerify/VerifyPeerCertificate — caught by
// checking Go's own documentation before this test was even written, but
// the fix itself was never proven by an actual handshake until this.
// Delete once the real Agent command server exists and exercises this
// for real.
package main

import (
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"os"
	"time"

	"tether/internal/certs"
	"tether/internal/trust"
)

const addr = "127.0.0.1:17422"

func startEchoServer(cfg *tls.Config, readyCh chan<- error, resultCh chan<- string) {
	listener, err := tls.Listen("tcp", addr, cfg)
	if err != nil {
		readyCh <- err
		return
	}
	readyCh <- nil
	defer listener.Close()

	conn, err := listener.Accept()
	if err != nil {
		resultCh <- fmt.Sprintf("accept error: %v", err)
		return
	}
	defer conn.Close()

	// Accept() on a tls.Listener returns before the handshake completes —
	// the handshake (and therefore our VerifyPeerCertificate check) only
	// actually runs on the first real I/O. Forcing it explicitly here
	// means we get a clean, immediate answer about whether verification
	// passed, rather than only discovering it indirectly via a later
	// read/write failure.
	tlsConn := conn.(*tls.Conn)
	if err := tlsConn.Handshake(); err != nil {
		resultCh <- fmt.Sprintf("handshake rejected: %v", err)
		return
	}

	buf := make([]byte, 64)
	n, err := conn.Read(buf)
	if err != nil && err != io.EOF {
		resultCh <- fmt.Sprintf("read error after successful handshake: %v", err)
		return
	}
	resultCh <- fmt.Sprintf("handshake succeeded, received: %q", string(buf[:n]))
}

func dialAndSend(cfg *tls.Config, message string) (string, error) {
	conn, err := tls.DialWithDialer(&net.Dialer{Timeout: 3 * time.Second}, "tcp", addr, cfg)
	if err != nil {
		return "", err
	}
	defer conn.Close()
	if _, err := conn.Write([]byte(message)); err != nil {
		return "", fmt.Errorf("connected but write failed: %w", err)
	}
	return "sent successfully", nil
}

func main() {
	agentIdentity, err := certs.LoadOrCreate("mtls-test-agent")
	if err != nil {
		panic(err)
	}
	orchestratorIdentity, err := certs.LoadOrCreate("mtls-test-orchestrator")
	if err != nil {
		panic(err)
	}

	// Simulate what pairing already established: each side has pinned
	// the other's cert. Using trust.Pin directly rather than running a
	// full pairing exchange — that flow is already proven by
	// pairing-e2e-test; this test's job is isolating PinnedTLSConfig
	// itself, not re-proving pairing.
	if err := trust.Pin("mtls-test-agent", agentIdentity.CertDER); err != nil {
		panic(err)
	}
	if err := trust.Pin("mtls-test-orchestrator", orchestratorIdentity.CertDER); err != nil {
		panic(err)
	}

	fmt.Println("=== Test 1: matching pins — real mTLS handshake should succeed ===")
	func() {
		serverCfg, err := trust.PinnedTLSConfig(agentIdentity.TLSCertificate(), "mtls-test-orchestrator", true)
		if err != nil {
			fmt.Printf("  FAIL: building server config: %v\n", err)
			return
		}
		clientCfg, err := trust.PinnedTLSConfig(orchestratorIdentity.TLSCertificate(), "mtls-test-agent", false)
		if err != nil {
			fmt.Printf("  FAIL: building client config: %v\n", err)
			return
		}

		readyCh := make(chan error, 1)
		resultCh := make(chan string, 1)
		go startEchoServer(serverCfg, readyCh, resultCh)
		if err := <-readyCh; err != nil {
			fmt.Printf("  FAIL: server failed to start listening: %v\n", err)
			return
		}

		sendResult, err := dialAndSend(clientCfg, "hello over real mTLS")
		if err != nil {
			fmt.Printf("  FAIL: client-side connection/handshake failed: %v\n", err)
		} else {
			fmt.Printf("  client: %s\n", sendResult)
		}

		select {
		case result := <-resultCh:
			if err == nil {
				fmt.Printf("  PASS: server: %s\n", result)
			} else {
				fmt.Printf("  server: %s\n", result)
			}
		case <-time.After(3 * time.Second):
			fmt.Println("  FAIL: server never reported a result (timed out)")
		}
	}()

	fmt.Println()
	fmt.Println("=== Test 2: mismatched pin — real mTLS handshake should be REJECTED ===")
	func() {
		// Server expects "mtls-test-orchestrator", but the actual client
		// connecting will present the AGENT's own cert instead — a
		// deliberate identity mismatch, simulating an attacker (or a
		// misconfigured/stale pin) presenting the wrong certificate.
		serverCfg, err := trust.PinnedTLSConfig(agentIdentity.TLSCertificate(), "mtls-test-orchestrator", true)
		if err != nil {
			fmt.Printf("  FAIL: building server config: %v\n", err)
			return
		}

		// Client presents the AGENT's cert (wrong identity for this
		// role) while still expecting to verify the server as the agent —
		// the mismatch we care about is what the SERVER sees from the
		// CLIENT, so what matters is that impostorClientCfg presents a
		// certificate that is NOT mtls-test-orchestrator.
		impostorClientCfg := &tls.Config{
			Certificates:       []tls.Certificate{agentIdentity.TLSCertificate()}, // wrong identity on purpose
			InsecureSkipVerify: true,
		}

		readyCh := make(chan error, 1)
		resultCh := make(chan string, 1)
		go startEchoServer(serverCfg, readyCh, resultCh)
		if err := <-readyCh; err != nil {
			fmt.Printf("  FAIL: server failed to start listening: %v\n", err)
			return
		}

		_, dialErr := dialAndSend(impostorClientCfg, "this should be rejected")
		// The rejection might surface on the client's side (handshake or
		// write fails outright) or only on the server's side (accepts
		// the TCP connection, and even lets an initial Write() appear to
		// succeed at the transport level, but rejects during the
		// explicit Handshake() call) — which one fires first depends on
		// timing/buffering and isn't what we actually care about. What
		// matters is a single, specific fact: did the SERVER'S
		// VerifyPeerCertificate callback ever accept the mismatched
		// cert. We check the server's own reported result for that,
		// rather than inferring anything from whether the client's Write
		// call happened to return without error — TCP-level write
		// buffering can let Write "succeed" even when the TLS layer
		// underneath is about to tear the connection down, so client-side
		// success is not proof the server accepted anything.
		select {
		case result := <-resultCh:
			if len(result) >= 17 && result[:17] == "handshake success" {
				fmt.Printf("  FAIL: server accepted the mismatched certificate: %s\n", result)
			} else {
				fmt.Printf("  PASS: server correctly rejected the mismatched certificate: %s\n", result)
			}
		case <-time.After(3 * time.Second):
			if dialErr != nil {
				fmt.Printf("  PASS: client-side rejection (server never got far enough to report): %v\n", dialErr)
			} else {
				fmt.Println("  FAIL: neither side reported a result, and no error occurred — unclear rejection")
			}
		}
	}()

	os.Exit(0)
}