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

package pairing

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"tether/internal/certs"
	"tether/internal/httpserver"
	"tether/internal/trust"
)

// maxPairRequestBytes bounds how much of a /pair request body we'll ever
// read. A pairing proof plus a PEM-encoded cert comfortably fits in a few
// KB; 64KB is generous headroom, not a tight budget. Without this, a
// client could stream an unbounded amount of data at the endpoint and
// exhaust memory before json.Decode ever gets far enough to reject it.
const maxPairRequestBytes = 64 * 1024

// shutdownTimeout bounds how long Start waits for in-flight connections
// to close gracefully before giving up. Without a timeout, a client that
// opens a connection and goes idle (deliberately or not) could make
// Shutdown block indefinitely, which defeats the point of a time-boxed
// pairing window.
const shutdownTimeout = 5 * time.Second

const (
	maxPairConnections = 16
	maxPairAttempts    = 5
	pairAttemptWindow  = time.Minute
)

// pairRequest is the JSON body an Orchestrator sends to an Agent's /pair
// endpoint. OrchestratorCertPEM is PEM-encoded (not raw DER) since JSON
// has no native binary type — PEM is already text, so it round-trips
// through JSON cleanly without a base64 wrapper layer of our own.
type pairRequest struct {
	PairingProof         string `json:"pairing_proof"`
	OrchestratorCertPEM  string `json:"orchestrator_cert"`
	OrchestratorHostname string `json:"orchestrator_hostname"`
}

// pairInfoResponse is unauthenticated bootstrap information. AgentProof is
// verified with the out-of-band code before the Client accepts AgentCertPEM.
type pairInfoResponse struct {
	AgentCertPEM string `json:"agent_cert"`
	AgentProof   string `json:"agent_proof"`
}

// pairResponse is what the Agent sends back on a successful pairing.
type pairResponse struct {
	AgentCertPEM string `json:"agent_cert"`
}

// pairErrorResponse is the JSON body sent on any failure, so a non-200
// response still has a machine-readable reason rather than just an HTTP
// status code and a plain-text body.
type pairErrorResponse struct {
	Error string `json:"error"`
}

// Server is the Agent-side pairing listener. It exists only while a
// pairing window is open (design doc §6.2) — construct one with NewServer
// and call Start, which blocks until the window closes or a pairing
// succeeds, whichever comes first. This is NOT a long-running server you
// start once at Agent boot and leave running; a fresh Server (and a fresh
// Code) should be created each time a human deliberately initiates
// pairing for a new node.
//
// Serves over TLS using the Agent's own identity certificate. The first
// bootstrap response is fetched before the Orchestrator has a pin, but its
// certificate is authenticated by an HMAC derived from the out-of-band code.
// The Client then pins that exact certificate for the pairing request. The
// code itself never crosses the network, and every connection after pairing
// uses the certificate trust.Pin stored for the peer.
type Server struct {
	code          *Code
	agentIdentity *certs.Identity
	window        time.Duration

	mu                          sync.Mutex
	result                      error // set once pairing completes or the window closes; nil result + done==true means success
	done                        bool
	doneCh                      chan struct{}
	orchestratorTailnetHostname string
	connections                 map[net.Conn]struct{}
	attempts                    map[string]pairAttempts
}

type pairAttempts struct {
	started time.Time
	count   int
}

// NewServer creates a pairing Server for a single pairing attempt, using
// agentIdentity as this Agent's own certificate. window controls both how long
// the generated Code
// stays valid AND how long Start listens before giving up — the two are
// intentionally the same value, driven from one parameter, so they can't
// drift apart (a Code that's still "valid" after the server has already
// shut down, or vice versa, would be a confusing state to debug). Real
// callers should pass pairing.Window; tests can pass a much shorter
// duration to avoid waiting out a real 2-minute expiry.
func NewServer(agentIdentity *certs.Identity, window time.Duration) (*Server, error) {
	code, err := GenerateWithWindow(window)
	if err != nil {
		return nil, fmt.Errorf("generating pairing code: %w", err)
	}
	return &Server{
		code:          code,
		agentIdentity: agentIdentity,
		window:        window,
		doneCh:        make(chan struct{}),
		connections:   make(map[net.Conn]struct{}),
		attempts:      make(map[string]pairAttempts),
	}, nil
}

// Code returns the pairing code for this session, for display to the
// user (the out-of-band step in §6.2 — a human reads this and types it
// into the Orchestrator, or vice versa depending on which side initiates
// display; see design doc for the full flow).
func (s *Server) Code() string {
	return s.code.String()
}

// OrchestratorTailnetHostname returns the peer hostname supplied during a
// successful code-gated pairing exchange for the Agent heartbeat.
func (s *Server) OrchestratorTailnetHostname() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.orchestratorTailnetHostname
}

// Start begins listening on addr (e.g. ":7420") and blocks until the
// pairing window closes or a pairing attempt succeeds — whichever comes
// first. Returns nil on a successful pairing, or an error describing why
// pairing did not complete (window expired with no valid attempt, or a
// server error).
//
// An invalid proof does not end the session, allowing a legitimate user to
// correct a mistyped code. Only a successful pairing, a server error, or the
// window's expiry ends the session.
func (s *Server) Start(addr string) error {
	mux := http.NewServeMux()
	mux.HandleFunc("/pair", s.handlePair)

	httpServer := &http.Server{
		Addr:    addr,
		Handler: mux,
		TLSConfig: &tls.Config{
			Certificates: []tls.Certificate{s.agentIdentity.TLSCertificate()},
		},
	}
	httpserver.Apply(httpServer)
	httpServer.WriteTimeout = 30 * time.Second
	httpServer.ConnState = s.limitConnections

	windowTimer := time.AfterFunc(s.window, func() {
		s.finish(fmt.Errorf("pairing window expired with no valid attempt"))
	})
	defer windowTimer.Stop()

	serverErrCh := make(chan error, 1)
	go func() {
		// Empty cert/key file arguments: the certificate is already
		// supplied via httpServer.TLSConfig.Certificates above, so there's
		// nothing to load from disk here. Using the already-parsed
		// in-memory identity instead of writing key material out to
		// temporary files avoids putting a copy of the private key
		// anywhere on disk beyond its one canonical location (managed by
		// internal/certs).
		if err := httpServer.ListenAndServeTLS("", ""); err != nil && err != http.ErrServerClosed {
			serverErrCh <- err
		}
	}()

	select {
	case <-s.doneCh:
		// Reached via a successful pairing or the window timer above —
		// either way, finish() has already set s.result.
	case err := <-serverErrCh:
		s.finish(fmt.Errorf("pairing server error: %w", err))
	}

	// Single Shutdown call site regardless of which branch above fired,
	// with a bounded timeout — without one, a client holding an idle
	// connection open could make this block indefinitely, which would
	// defeat the entire point of a time-boxed pairing window.
	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		log.Printf("pairing: server shutdown did not complete cleanly: %v", err)
	}

	return s.result
}

// handlePair returns code-authenticated Agent bootstrap information on GET and
// accepts a certificate-bound pairing proof on POST. The out-of-band code is
// never sent over the network.
//
// A WRONG code rejects the request but does NOT call finish — the
// session stays alive for another attempt (see Start's doc comment). Once
// Consume returns true (a correct code was presented), the code is
// already burned by definition — anything that fails AFTER that point
// (malformed cert, pin failure) DOES call finish, since no further
// attempt against this Server could succeed regardless: the one valid
// code has already been used.
func (s *Server) handlePair(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		s.handlePairInfo(w)
		return
	}
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "only POST is supported")
		return
	}
	if !s.allowAttempt(r.RemoteAddr) {
		w.Header().Set("Retry-After", "60")
		writeError(w, http.StatusTooManyRequests, "too many pairing attempts; wait and try again")
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxPairRequestBytes)

	var req pairRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		var maxBytesErr *http.MaxBytesError
		if errors.As(err, &maxBytesErr) {
			writeError(w, http.StatusRequestEntityTooLarge, "request body too large")
		} else {
			writeError(w, http.StatusBadRequest, "malformed request body")
		}
		return
	}

	block, _ := pem.Decode([]byte(req.OrchestratorCertPEM))
	if block == nil {
		writeError(w, http.StatusBadRequest, "no PEM block found in orchestrator_cert")
		return
	}

	orchestratorCert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		writeError(w, http.StatusBadRequest, "orchestrator_cert did not parse as a valid certificate")
		return
	}

	orchestratorHostname := orchestratorCert.Subject.CommonName
	if orchestratorHostname == "" {
		writeError(w, http.StatusBadRequest, "orchestrator_cert has no CommonName to identify it by")
		return
	}

	if req.OrchestratorHostname == "" || strings.ContainsAny(req.OrchestratorHostname, "/\\ \t\r\n") {
		writeError(w, http.StatusBadRequest, "orchestrator Tailnet hostname is invalid")
		return
	}
	if !s.code.ConsumeProof(req.PairingProof, s.agentIdentity.CertDER, block.Bytes, []byte(req.OrchestratorHostname)) {
		writeError(w, http.StatusUnauthorized, "invalid or expired pairing proof")
		log.Printf("pairing: rejected an attempt with an invalid or expired pairing proof")
		return
	}
	if err := trust.Pin(orchestratorHostname, block.Bytes); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to pin orchestrator certificate")
		s.finish(fmt.Errorf("pairing proof succeeded but pinning orchestrator cert failed: %w", err))
		return
	}
	s.mu.Lock()
	s.orchestratorTailnetHostname = req.OrchestratorHostname
	s.mu.Unlock()

	resp := pairResponse{
		AgentCertPEM: string(pem.EncodeToMemory(&pem.Block{
			Type:  "CERTIFICATE",
			Bytes: s.agentIdentity.CertDER,
		})),
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		// The pin already succeeded and was persisted at this point — a
		// failure writing the HTTP response doesn't undo that. Logged
		// rather than surfaced to s.finish as a pairing failure, since
		// from the Agent's own state, pairing genuinely did succeed; the
		// Orchestrator side may need to retry fetching the Agent's cert
		// some other way, but that's a follow-up concern, not grounds to
		// call this pairing attempt itself a failure.
		log.Printf("pairing: succeeded but failed to write response: %v", err)
	}

	log.Printf("pairing: successfully paired with orchestrator %q", orchestratorHostname)
	s.finish(nil)
}

func (s *Server) handlePairInfo(w http.ResponseWriter) {
	certificatePEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: s.agentIdentity.CertDER})
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(pairInfoResponse{
		AgentCertPEM: string(certificatePEM),
		AgentProof:   s.code.Proof(s.agentIdentity.CertDER),
	}); err != nil {
		log.Printf("pairing: failed to write pairing information: %v", err)
	}
}

// limitConnections caps the pairing window's exposure before a request is
// even parsed. Header and read deadlines handle slow clients; this bound keeps
// a peer from consuming unbounded sockets during the unauthenticated phase.
func (s *Server) limitConnections(conn net.Conn, state http.ConnState) {
	s.mu.Lock()
	closeConnection := false
	switch state {
	case http.StateNew:
		if len(s.connections) >= maxPairConnections {
			closeConnection = true
		} else {
			s.connections[conn] = struct{}{}
		}
	case http.StateClosed, http.StateHijacked:
		delete(s.connections, conn)
	}
	s.mu.Unlock()
	if closeConnection {
		_ = conn.Close()
	}
}

// allowAttempt permits a few code submissions per source each minute. The
// pairing code remains retryable for a human typo, while online guessing is
// rate limited and the small map is bounded by the short pairing window.
func (s *Server) allowAttempt(remoteAddr string) bool {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		host = remoteAddr
	}
	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	attempt := s.attempts[host]
	if attempt.started.IsZero() || now.Sub(attempt.started) >= pairAttemptWindow {
		attempt = pairAttempts{started: now}
	}
	if attempt.count >= maxPairAttempts {
		s.attempts[host] = attempt
		return false
	}
	attempt.count++
	s.attempts[host] = attempt
	return true
}

// finish records the final result and signals doneCh exactly once —
// guarded by mu since it can be called from either the window-expiry
// timer's goroutine or handlePair's goroutine (whichever happens first),
// and a channel can only be closed once; closing it twice panics.
func (s *Server) finish(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.done {
		return
	}
	s.done = true
	s.result = err
	close(s.doneCh)
}

func writeError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(pairErrorResponse{Error: message})
}
