// Package agent implements the Agent's ongoing command server — the
// mTLS-secured channel the Orchestrator uses, after pairing, to tell an
// Agent to start/stop/check its local llama.cpp rpc-server process
// (design doc §5 step 3). This is deliberately a separate package from
// internal/pairing: pairing is a one-time bootstrap that establishes
// trust; this is the persistent channel that trust exists to secure.
// Conflating them would mix a "prove who you are, once" concern with an
// "act on already-established trust, repeatedly" concern.
package agent

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"

	"tether/internal/config"
	"tether/internal/process"
)

const maxRequestBytes = 64 * 1024

// shutdownTimeout bounds graceful shutdown, same reasoning as
// pairing.Server: without a bound, an idle client connection could make
// Shutdown block indefinitely.
const shutdownTimeout = 5 * time.Second

type startRequest struct {
	Port int `json:"port"`
}

type statusResponse struct {
	Status    string `json:"status"`
	LastError string `json:"last_error,omitempty"`
}

type errorResponse struct {
	Error string `json:"error"`
}

// Server is the Agent's ongoing mTLS command server. Unlike
// pairing.Server (which exists only for the duration of one pairing
// attempt and shuts itself down via an internal timer), Server is meant
// to run for as long as the Agent itself runs — construct once at Agent
// startup, call Start with a cancellable context, and it serves until
// that context is cancelled.
type Server struct {
	config  *agentconfig.Config
	manager *process.Manager
}

// NewServer creates an agent command Server using cfg for the approved
// binary path and model list, and manager for process lifecycle. manager
// is accepted as a parameter (rather than Server creating its own) so a
// caller can hold onto the same *process.Manager for other purposes
// (e.g. querying status outside of an HTTP request) if that's ever
// needed — Server doesn't need to be the sole owner of process state.
func NewServer(cfg *agentconfig.Config, manager *process.Manager) *Server {
	return &Server{config: cfg, manager: manager}
}

// Start begins serving on addr using tlsConfig — build tlsConfig with
// trust.PinnedTLSConfig(selfIdentity, orchestratorHostname, true) so
// every connection is genuinely mutually authenticated against the
// specific cert pairing pinned, not just "some Orchestrator." Blocks
// until ctx is cancelled, then shuts down gracefully within
// shutdownTimeout.
//
// tls.Config is accepted directly here, rather than this package
// depending on internal/trust itself, to keep agent's own dependency
// graph limited to what it actually needs (HTTP serving + process
// control) — building the right *tls.Config is the caller's job
// (cmd/tether-agent), using internal/trust and internal/certs directly,
// exactly the way cmd/tether-agent already builds a pairing.Server.
func (s *Server) Start(ctx context.Context, addr string, tlsConfig *tls.Config) error {
	mux := http.NewServeMux()
	mux.HandleFunc("/start", s.handleStart)
	mux.HandleFunc("/stop", s.handleStop)
	mux.HandleFunc("/status", s.handleStatus)

	httpServer := &http.Server{
		Addr:      addr,
		Handler:   mux,
		TLSConfig: tlsConfig,
	}

	serverErrCh := make(chan error, 1)
	go func() {
		if err := httpServer.ListenAndServeTLS("", ""); err != nil && err != http.ErrServerClosed {
			serverErrCh <- err
		}
	}()

	select {
	case <-ctx.Done():
		// normal path: caller wants this server to stop.
	case err := <-serverErrCh:
		return fmt.Errorf("agent command server error: %w", err)
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	return httpServer.Shutdown(shutdownCtx)
}

func (s *Server) handleStart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "only POST is supported")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxRequestBytes)

	var req startRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "malformed request body")
		return
	}

	if req.Port < 1 || req.Port > 65535 {
		writeError(w, http.StatusBadRequest, "port must be between 1 and 65535")
		return
	}

	err := s.manager.Start(process.StartParams{
		BinaryPath: s.config.RPCServerPath,
		Host:       s.config.RPCListenHost,
		Port:       req.Port,
	})
	if err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}

	log.Printf("agent: started ggml-rpc-server (host=%q port=%d)", s.config.RPCListenHost, req.Port)
	writeStatus(w, s.manager)
}

func (s *Server) handleStop(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "only POST is supported")
		return
	}

	if err := s.manager.StopDefault(); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	log.Println("agent: stopped ggml-rpc-server")
	writeStatus(w, s.manager)
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "only GET is supported")
		return
	}
	writeStatus(w, s.manager)
}

func writeStatus(w http.ResponseWriter, m *process.Manager) {
	resp := statusResponse{Status: m.Status().String()}
	if err := m.LastExitError(); err != nil {
		resp.LastError = err.Error()
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(resp)
}

func writeError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(errorResponse{Error: message})
}
