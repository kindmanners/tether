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

// Command tether-api starts Tether's OpenAI-compatible inference gateway.
// It owns one llama.cpp worker per loaded model, so every model load can make
// a fresh VRAM-aware RPC placement decision. Open WebUI and other clients see
// one ordinary /v1 API while Tether owns worker lifecycle and idle eviction.
package main

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"tether/internal/httpserver"
)

const orchestratorIdentityName = "orchestrator"

func main() {
	defaultModelsDir := "models"
	if home, err := os.UserHomeDir(); err == nil {
		defaultModelsDir = filepath.Join(home, "models")
	}

	defaultLlamaServer := defaultLlamaServerPath(false)
	cudaLlamaServer := defaultLlamaServerPath(true)
	listen := flag.String("listen", "127.0.0.1:11435", "OpenAI API address (host:port)")
	modelsDir := flag.String("models-dir", defaultModelsDir, "directory containing GGUF models")
	llamaServer := flag.String("llama-server", "", "path to llama.cpp llama-server")
	localGPURequested := flag.Bool("local-gpu", true, "allow this Orchestrator to contribute its local CUDA GPU")
	agentsFlag := flag.String("agents", "auto", "paired Agent hostnames separated by commas, auto, or none")
	legacyRPC := flag.String("rpc", "", "deprecated alias for --agents; raw host:port endpoints are rejected")
	allowlistPath := flag.String("allowlist", "node_allowlist.yaml", "path to Tether node allowlist for Agent selection")
	apiKey := flag.String("api-key", os.Getenv("TETHER_API_KEY"), "API key required by every client (or set TETHER_API_KEY)")
	ctxSize := flag.Int("ctx-size", 8192, "context size per loaded model")
	parallel := flag.Int("parallel", 2, "parallel requests per loaded model")
	idleUnload := flag.Duration("idle-unload", 5*time.Minute, "unload an idle model worker after this duration; 0 disables idle unload")
	workerStartTimeout := flag.Duration("worker-start-timeout", 5*time.Minute, "maximum time to wait for a model worker to load and become healthy")
	modelOverhead := flag.Float64("model-overhead", 1.15, "multiply GGUF file size by this runtime memory reserve")
	kvBytesPerToken := flag.Int64("kv-cache-bytes-per-token", 256*1024, "conservative KV-cache VRAM reserve per context token")
	flag.Parse()
	selectionValue := *agentsFlag
	legacySet, agentsSet := false, false
	flag.Visit(func(f *flag.Flag) {
		legacySet = legacySet || f.Name == "rpc"
		agentsSet = agentsSet || f.Name == "agents"
	})
	if legacySet {
		if agentsSet {
			log.Fatal("use only --agents; --rpc is a deprecated alias")
		}
		selectionValue = *legacyRPC
		log.Printf("WARNING: --rpc is deprecated; use --agents with paired Agent hostnames")
	}
	selection, err := parseAgentSelection(selectionValue)
	if err != nil {
		log.Fatalf("invalid Agent selection: %v", err)
	}
	if *llamaServer == "" {
		if *localGPURequested {
			if info, err := os.Stat(cudaLlamaServer); err == nil && !info.IsDir() {
				defaultLlamaServer = cudaLlamaServer
			}
		}
		*llamaServer = defaultLlamaServer
	}

	host, port, err := net.SplitHostPort(*listen)
	if err != nil || host == "" || port == "" {
		log.Fatalf("-listen must be a host:port, for example 127.0.0.1:11435")
	}
	if *apiKey == "" {
		log.Fatal("-api-key or TETHER_API_KEY is required")
	}
	if *ctxSize < 1 || *parallel < 1 || *idleUnload < 0 || *workerStartTimeout <= 0 || *modelOverhead < 1 || *kvBytesPerToken < 0 {
		log.Fatal("-ctx-size, -parallel, and -worker-start-timeout must be positive; -idle-unload and -kv-cache-bytes-per-token cannot be negative; -model-overhead must be at least 1")
	}
	if err := os.MkdirAll(*modelsDir, 0700); err != nil {
		log.Fatalf("creating model directory %q: %v", *modelsDir, err)
	}
	if info, err := os.Stat(*modelsDir); err != nil || !info.IsDir() {
		log.Fatalf("model directory %q is unavailable: %v", *modelsDir, err)
	}
	if info, err := os.Stat(*llamaServer); err != nil || info.IsDir() {
		log.Fatalf("llama-server at %q is unavailable: %v", *llamaServer, err)
	}
	localGPU := false
	if *localGPURequested {
		var err error
		localGPU, err = llamaServerHasCUDA(*llamaServer)
		if err != nil {
			log.Fatalf("checking local llama-server CUDA backend: %v", err)
		}
	}

	shutdownRequested := make(chan struct{}, 1)
	gateway, err := newGateway(gatewayConfig{
		modelsDir: *modelsDir, llamaServer: *llamaServer, agentSelection: selection, allowlistPath: *allowlistPath,
		apiKey: *apiKey, ctxSize: *ctxSize, parallel: *parallel, idleTimeout: *idleUnload, workerStartTimeout: *workerStartTimeout, localGPU: localGPU,
		modelOverhead: *modelOverhead, kvBytesPerToken: *kvBytesPerToken,
		requestShutdown: func() {
			select {
			case shutdownRequested <- struct{}{}:
			default:
			}
		},
	})
	if err != nil {
		log.Fatalf("loading model library: %v", err)
	}
	server := &http.Server{Addr: *listen, Handler: gateway}
	httpserver.Apply(server)
	serverErr := make(chan error, 1)
	go func() {
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			serverErr <- err
		}
	}()

	log.Printf("Tether API listening on http://%s/v1", *listen)
	log.Printf("OpenAI routes: GET /v1/models, POST /v1/chat/completions")
	if selection.mode == agentsAuto {
		log.Printf("RPC placement: whole-model GPU when it fits; RPC mesh only when required")
	}
	if !*localGPURequested {
		log.Printf("Local GPU contribution disabled by Orchestrator preference")
	} else if !localGPU {
		log.Printf("Local GPU placement disabled: %s does not expose a CUDA backend", *llamaServer)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	select {
	case err := <-serverErr:
		log.Fatalf("Tether API stopped: %v", err)
	case <-ctx.Done():
	case <-shutdownRequested:
		log.Printf("Tether API received local shutdown request")
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = server.Shutdown(shutdownCtx)
	gateway.shutdown(shutdownCtx)
}

func defaultLlamaServerPath(cuda bool) string {
	build := "build-rpc"
	if cuda {
		build = "build-rpc-cuda"
	}
	parts := []string{"llama.cpp", build, "bin"}
	name := "llama-server"
	if runtime.GOOS == "windows" {
		parts = append(parts, "Release")
		name += ".exe"
	}
	return filepath.Join(append(parts, name)...)
}

func llamaServerHasCUDA(path string) (bool, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	output, err := exec.CommandContext(ctx, path, "--list-devices").CombinedOutput()
	if ctx.Err() != nil {
		return false, fmt.Errorf("timed out running --list-devices")
	}
	if err != nil {
		return false, fmt.Errorf("running --list-devices: %w: %s", err, strings.TrimSpace(string(output)))
	}
	return bytes.Contains(bytes.ToLower(output), []byte("cuda")), nil
}

func isLoopbackHost(host string) bool {
	host = strings.Trim(host, "[]")
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
