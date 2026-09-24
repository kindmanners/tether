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
	"crypto/tls"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"tether/internal/config"
	"tether/internal/executil"
	"tether/internal/httpserver"
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
	mux.HandleFunc("/capabilities", s.handleCapabilities)

	httpServer := &http.Server{
		Addr:         addr,
		Handler:      mux,
		TLSConfig:    tlsConfig,
		WriteTimeout: 30 * time.Second,
	}
	httpserver.Apply(httpServer)

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
