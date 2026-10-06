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
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"tether/internal/config"
	"tether/internal/executil"
	"tether/internal/httpserver"
	"tether/internal/process"
)

const maxRequestBytes = 64 * 1024

const (
	maxRPCStreamsPerPeer = 8
	maxRPCStreamsTotal   = 16
	rpcConnectTimeout    = 10 * time.Second
)

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

// GPUCapability is the GPU information an Agent records locally during
// bootstrap. It is served only through the already-pinned mTLS channel; the
// public tailnet never receives this report.
type GPUCapability struct {
	Name               string `json:"name"`
	DriverVersion      string `json:"driverVersion"`
	VRAMBytes          int64  `json:"vramBytes"`
	VRAMFreeBytes      int64  `json:"vramFreeBytes,omitempty"`
	UtilizationPercent int    `json:"utilizationPercent,omitempty"`
}

// CapabilitiesResult is the small, machine-observed report used by the
// Orchestrator dashboard. The Agent does not invent values here: the Windows
// bootstrapper writes the report from the local operating system.
type CapabilitiesResult struct {
	ObservedAt string          `json:"observedAt"`
	Hostname   string          `json:"hostname"`
	CUDA       string          `json:"cudaVersion"`
	GPUs       []GPUCapability `json:"gpus"`
}

// Server is the Agent's ongoing mTLS command server. Unlike
// pairing.Server (which exists only for the duration of one pairing
// attempt and shuts itself down via an internal timer), Server is meant
// to run for as long as the Agent itself runs — construct once at Agent
// startup, call Start with a cancellable context, and it serves until
// that context is cancelled.
type Server struct {
	config         *agentconfig.Config
	manager        *process.Manager
	streamMu       sync.Mutex
	streams        map[*rpcStream]struct{}
	peerStreams    map[[sha256.Size]byte]int
	totalStreams   int
	closing        bool
	shutdownCtx    context.Context
	cancelShutdown context.CancelFunc
	streamWG       sync.WaitGroup
}

type rpcStream struct {
	server  *Server
	peer    [sha256.Size]byte
	client  net.Conn
	backend *process.RPCLease
	once    sync.Once
}

// NewServer creates an agent command Server using cfg for the approved
// binary path and model list, and manager for process lifecycle. manager
// is accepted as a parameter (rather than Server creating its own) so a
// caller can hold onto the same *process.Manager for other purposes
// (e.g. querying status outside of an HTTP request) if that's ever
// needed — Server doesn't need to be the sole owner of process state.
func NewServer(cfg *agentconfig.Config, manager *process.Manager) *Server {
	shutdownCtx, cancelShutdown := context.WithCancel(context.Background())
	return &Server{
		config: cfg, manager: manager, streams: make(map[*rpcStream]struct{}),
		peerStreams: make(map[[sha256.Size]byte]int), shutdownCtx: shutdownCtx, cancelShutdown: cancelShutdown,
	}
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
	serverTLS := tlsConfig.Clone()
	serverTLS.NextProtos = []string{"http/1.1"}
	httpServer := &http.Server{
		Addr:         addr,
		Handler:      s.handler(),
		TLSConfig:    serverTLS,
		WriteTimeout: 30 * time.Second,
		TLSNextProto: make(map[string]func(*http.Server, *tls.Conn, http.Handler)),
	}
	httpserver.Apply(httpServer)

	serverErrCh := make(chan error, 1)
	go func() {
		if err := httpServer.ListenAndServeTLS("", ""); err != nil && err != http.ErrServerClosed {
			serverErrCh <- err
		}
	}()

	var serveErr error
	select {
	case <-ctx.Done():
	case err := <-serverErrCh:
		serveErr = fmt.Errorf("agent command server error: %w", err)
	}

	s.beginShutdown()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	shutdownErr := httpServer.Shutdown(shutdownCtx)
	s.streamWG.Wait()
	if serveErr != nil {
		return serveErr
	}
	return shutdownErr
}

func (s *Server) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/start", s.handleStart)
	mux.HandleFunc("/stop", s.handleStop)
	mux.HandleFunc("/status", s.handleStatus)
	mux.HandleFunc("/capabilities", s.handleCapabilities)
	mux.HandleFunc("/rpc", s.handleRPC)
	return mux
}

func (s *Server) handleRPC(w http.ResponseWriter, r *http.Request) {
	hasBody := r.ContentLength != 0 || len(r.TransferEncoding) != 0 || (r.Body != nil && r.Body != http.NoBody)
	hasDestinationHeader := r.Header.Get("X-Tether-RPC-Target") != "" ||
		r.Header.Get("X-Tether-RPC-Destination") != "" ||
		r.Header.Get("X-Tether-RPC-Port") != ""
	if r.Method != http.MethodConnect || r.ProtoMajor != 1 || r.URL.Path != "/rpc" || r.URL.RawQuery != "" || r.RequestURI != "/rpc" || hasBody || hasDestinationHeader {
		writeError(w, http.StatusBadRequest, "RPC tunneling requires HTTP/1.1 CONNECT /rpc with no destination, query, or body")
		return
	}
	if r.TLS == nil || len(r.TLS.PeerCertificates) != 1 {
		writeError(w, http.StatusUnauthorized, "a paired client certificate is required")
		return
	}
	peer := sha256.Sum256(r.TLS.PeerCertificates[0].Raw)
	if !s.reserveStream(peer) {
		writeError(w, http.StatusTooManyRequests, "RPC stream limit reached")
		return
	}
	reserved := true
	defer func() {
		if reserved {
			s.releaseReservation(peer)
		}
	}()

	connectCtx, cancel := context.WithTimeout(r.Context(), rpcConnectTimeout)
	stopCancel := context.AfterFunc(s.shutdownCtx, cancel)
	backend, err := s.manager.AcquireRPC(connectCtx)
	stopCancel()
	cancel()
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, err.Error())
		return
	}

	hijacker, ok := w.(http.Hijacker)
	if !ok {
		_ = backend.Close()
		writeError(w, http.StatusHTTPVersionNotSupported, "RPC tunneling requires HTTP/1.1")
		return
	}
	client, buffered, err := hijacker.Hijack()
	if err != nil {
		_ = backend.Close()
		return
	}
	_ = client.SetDeadline(time.Time{})
	_ = backend.SetDeadline(time.Time{})
	stream := &rpcStream{server: s, peer: peer, client: client, backend: backend}
	if !s.registerStream(stream) {
		_ = client.Close()
		_ = backend.Close()
		return
	}
	reserved = false

	if _, err := buffered.WriteString("HTTP/1.1 200 Connection Established\r\n\r\n"); err != nil || buffered.Flush() != nil {
		stream.close()
		return
	}
	defer s.streamWG.Done()
	done := make(chan struct{}, 2)
	go func() {
		_, _ = io.Copy(backend, buffered.Reader)
		closeWrite(backend)
		done <- struct{}{}
	}()
	go func() {
		_, _ = io.Copy(client, backend)
		closeWrite(client)
		done <- struct{}{}
	}()
	<-done
	stream.close()
	<-done
}

func closeWrite(conn net.Conn) {
	if closer, ok := conn.(interface{ CloseWrite() error }); ok {
		_ = closer.CloseWrite()
	}
}

func (s *Server) reserveStream(peer [sha256.Size]byte) bool {
	s.streamMu.Lock()
	defer s.streamMu.Unlock()
	if s.closing || s.totalStreams >= maxRPCStreamsTotal || s.peerStreams[peer] >= maxRPCStreamsPerPeer {
		return false
	}
	s.totalStreams++
	s.peerStreams[peer]++
	return true
}

func (s *Server) releaseReservation(peer [sha256.Size]byte) {
	s.streamMu.Lock()
	defer s.streamMu.Unlock()
	s.totalStreams--
	s.peerStreams[peer]--
	if s.peerStreams[peer] == 0 {
		delete(s.peerStreams, peer)
	}
}

func (s *Server) registerStream(stream *rpcStream) bool {
	s.streamMu.Lock()
	defer s.streamMu.Unlock()
	if s.closing {
		return false
	}
	s.streams[stream] = struct{}{}
	s.streamWG.Add(1)
	return true
}

func (s *Server) beginShutdown() {
	s.streamMu.Lock()
	if s.closing {
		s.streamMu.Unlock()
		return
	}
	s.closing = true
	s.cancelShutdown()
	streams := make([]*rpcStream, 0, len(s.streams))
	for stream := range s.streams {
		streams = append(streams, stream)
	}
	s.streamMu.Unlock()
	for _, stream := range streams {
		stream.close()
	}
}

func (s *rpcStream) close() {
	s.once.Do(func() {
		_ = s.client.Close()
		_ = s.backend.Close()
		s.server.streamMu.Lock()
		delete(s.server.streams, s)
		s.server.totalStreams--
		s.server.peerStreams[s.peer]--
		if s.server.peerStreams[s.peer] == 0 {
			delete(s.server.peerStreams, s.peer)
		}
		s.server.streamMu.Unlock()
	})
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

// handleCapabilities returns the local bootstrap report, if one exists. A
// missing report is deliberately visible as 404 instead of a zero-value GPU:
// callers must distinguish "the node reports no VRAM" from "this node has not
// been bootstrapped yet."
func (s *Server) handleCapabilities(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "only GET is supported")
		return
	}

	dir, err := agentconfig.DefaultDirectory()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "locating Agent state: "+err.Error())
		return
	}

	data, err := os.ReadFile(filepath.Join(dir, "bootstrap-report.json"))
	if err != nil {
		if os.IsNotExist(err) {
			writeError(w, http.StatusNotFound, "no bootstrap capability report is available on this node")
			return
		}
		writeError(w, http.StatusInternalServerError, "reading bootstrap capability report: "+err.Error())
		return
	}

	// Windows PowerShell 5.1's UTF8 writer prepends a UTF-8 BOM. JSON itself
	// does not permit that marker, but accepting it here makes an existing
	// bootstrap report usable while the bootstrapper migrates to BOM-free UTF-8.
	data = bytes.TrimPrefix(data, []byte{0xEF, 0xBB, 0xBF})
	var report CapabilitiesResult
	if err := json.Unmarshal(data, &report); err != nil {
		writeError(w, http.StatusInternalServerError, "parsing bootstrap capability report: "+err.Error())
		return
	}
	// Bootstrap proves the hardware/toolchain exists. Placement needs current
	// free VRAM, so refresh from the NVIDIA driver at request time whenever the
	// management tool is available. Keep the bootstrapped report on failure:
	// availability of telemetry must not make a paired Agent unusable.
	if gpus, err := liveNvidiaGPUs(); err == nil && len(gpus) > 0 {
		report.GPUs = gpus
		report.ObservedAt = time.Now().UTC().Format(time.RFC3339)
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(report)
}

func liveNvidiaGPUs() ([]GPUCapability, error) {
	cmd := exec.Command("nvidia-smi", "--query-gpu=name,memory.total,memory.free,utilization.gpu,driver_version", "--format=csv,noheader,nounits")
	executil.HideWindow(cmd)
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("running nvidia-smi: %w", err)
	}
	lines := strings.FieldsFunc(string(output), func(r rune) bool { return r == '\n' || r == '\r' })
	gpus := make([]GPUCapability, 0, len(lines))
	for _, line := range lines {
		fields := strings.Split(line, ",")
		if len(fields) != 5 {
			return nil, fmt.Errorf("unexpected nvidia-smi row %q", line)
		}
		totalMiB, err := strconv.ParseInt(strings.TrimSpace(fields[1]), 10, 64)
		if err != nil {
			return nil, fmt.Errorf("parsing total VRAM: %w", err)
		}
		freeMiB, err := strconv.ParseInt(strings.TrimSpace(fields[2]), 10, 64)
		if err != nil {
			return nil, fmt.Errorf("parsing free VRAM: %w", err)
		}
		utilization, err := strconv.Atoi(strings.TrimSpace(fields[3]))
		if err != nil {
			return nil, fmt.Errorf("parsing GPU utilization: %w", err)
		}
		gpus = append(gpus, GPUCapability{Name: strings.TrimSpace(fields[0]), DriverVersion: strings.TrimSpace(fields[4]), VRAMBytes: totalMiB * 1024 * 1024, VRAMFreeBytes: freeMiB * 1024 * 1024, UtilizationPercent: utilization})
	}
	return gpus, nil
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
