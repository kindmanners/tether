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
	"net/http"
	"sync"
	"time"

	"tether/internal/certs"
	"tether/internal/trust"
)

// maxPairRequestBytes bounds how much of a /pair request body we'll ever
// read. A pairing_code plus a PEM-encoded cert comfortably fits in a few
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

// pairRequest is the JSON body an Orchestrator sends to an Agent's /pair
// endpoint. OrchestratorCertPEM is PEM-encoded (not raw DER) since JSON
// has no native binary type — PEM is already text, so it round-trips
// through JSON cleanly without a base64 wrapper layer of our own.
type pairRequest struct {
	PairingCode         string `json:"pairing_code"`
	OrchestratorCertPEM string `json:"orchestrator_cert"`
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
// Serves over TLS using the Agent's own identity certificate (from
// internal/certs). This is NOT mutual/verified TLS — the Orchestrator has
// no pinned cert for this Agent yet at this point (that's the entire
// problem pairing solves), so the Orchestrator-side client for this
// specific bootstrap call must skip certificate verification. That's
// safe here specifically because the pairing Code, not the TLS
// certificate, is the actual authentication mechanism during pairing —
// TLS at this stage buys confidentiality-in-transit against passive
// network observation, not identity verification. Every connection AFTER
// a successful pairing uses the cert trust.Pin stored, verified for
// real — InsecureSkipVerify must never be used there.
type Server struct {
	code          *Code
	agentIdentity *certs.Identity

	mu     sync.Mutex
	result error // set once pairing completes or the window closes; nil result + done==true means success
	done   bool
	doneCh chan struct{}
}

// NewServer creates a pairing Server for a single pairing attempt, using
// agentIdentity as this Agent's own certificate to present once a valid
// code is received.
func NewServer(agentIdentity *certs.Identity) (*Server, error) {
	code, err := Generate()
	if err != nil {
		return nil, fmt.Errorf("generating pairing code: %w", err)
	}
	return &Server{
		code:          code,
		agentIdentity: agentIdentity,
		doneCh:        make(chan struct{}),
	}, nil
}

// Code returns the pairing code for this session, for display to the
// user (the out-of-band step in §6.2 — a human reads this and types it
// into the Orchestrator, or vice versa depending on which side initiates
// display; see design doc for the full flow).
func (s *Server) Code() string {
	return s.code.String()
}

// Start begins listening on addr (e.g. ":7420") and blocks until the
// pairing window closes or a pairing attempt succeeds — whichever comes
// first. Returns nil on a successful pairing, or an error describing why
// pairing did not complete (window expired with no valid attempt, or a
// server error).
//
// A WRONG pairing code does NOT end the session — Start keeps listening,
// allowing the legitimate user to retry, right up until either a correct
// code is presented or the window elapses. See Code.Consume's doc
// comment for why: an earlier version terminated the session on the
// first wrong guess, which meant anyone who could reach this port (not
// just the legitimate user) could kill a pairing attempt with a single
// garbage request — a trivial denial-of-service against the exact thing
// this mechanism exists to protect. Only a SUCCESSFUL pairing, a server
// error, or the window's own expiry end the session now.
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

	windowTimer := time.AfterFunc(Window, func() {
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

// handlePair is the actual /pair endpoint logic: validate the code,
// parse and pin the Orchestrator's cert, respond with this Agent's own
// cert.
//
// A WRONG code rejects the request but does NOT call finish — the
// session stays alive for another attempt (see Start's doc comment). Once
// Consume returns true (a correct code was presented), the code is
// already burned by definition — anything that fails AFTER that point
// (malformed cert, pin failure) DOES call finish, since no further
// attempt against this Server could succeed regardless: the one valid
// code has already been used.
func (s *Server) handlePair(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "only POST is supported")
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

	if !s.code.Consume(req.PairingCode) {
		// Deliberately vague to the caller — "invalid or expired code"
		// rather than distinguishing "wrong code" from "expired" from
		// "already used by someone else". A more specific error would
		// help a legitimate user debug a typo, but would also help an
		// attacker distinguish attack outcomes; the fix is identical in
		// every case (try again, or ask for a fresh code once expired),
		// so the vaguer message costs little. The candidate code itself
		// is never logged — design doc §6.2 says pairing codes are never
		// persisted or logged, and that applies to failed guesses too,
		// not just the real one.
		writeError(w, http.StatusUnauthorized, "invalid or expired pairing code")
		log.Printf("pairing: rejected an attempt with an invalid or expired code")
		return // NOT calling finish — session stays open for a retry
	}

	block, _ := pem.Decode([]byte(req.OrchestratorCertPEM))
	if block == nil {
		writeError(w, http.StatusBadRequest, "no PEM block found in orchestrator_cert")
		s.finish(fmt.Errorf("pairing succeeded on code but orchestrator cert was malformed (no PEM block)"))
		return
	}

	orchestratorCert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		writeError(w, http.StatusBadRequest, "orchestrator_cert did not parse as a valid certificate")
		s.finish(fmt.Errorf("pairing succeeded on code but orchestrator cert failed to parse: %w", err))
		return
	}

	orchestratorHostname := orchestratorCert.Subject.CommonName
	if orchestratorHostname == "" {
		writeError(w, http.StatusBadRequest, "orchestrator_cert has no CommonName to identify it by")
		s.finish(fmt.Errorf("pairing succeeded on code but orchestrator cert had empty CommonName"))
		return
	}

	if err := trust.Pin(orchestratorHostname, block.Bytes); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to pin orchestrator certificate")
		s.finish(fmt.Errorf("pairing succeeded on code but pinning orchestrator cert failed: %w", err))
		return
	}

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