// Command tether-api starts Tether's OpenAI-compatible inference gateway.
// It owns one llama.cpp worker per loaded model, so every model load can make
// a fresh VRAM-aware RPC placement decision. Open WebUI and other clients see
// one ordinary /v1 API while Tether owns worker lifecycle and idle eviction.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"tether/internal/agent"
	"tether/internal/certs"
	"tether/internal/registry"
	"tether/internal/trust"
)

const orchestratorIdentityName = "orchestrator"

func main() {
	defaultModelsDir := "models"
	if home, err := os.UserHomeDir(); err == nil {
		defaultModelsDir = filepath.Join(home, "models")
	}

	listen := flag.String("listen", "127.0.0.1:11435", "OpenAI API address (host:port)")
	modelsDir := flag.String("models-dir", defaultModelsDir, "directory containing GGUF models")
	llamaServer := flag.String("llama-server", "llama.cpp/build-rpc/bin/llama-server", "path to llama.cpp llama-server")
	rpc := flag.String("rpc", "auto", "comma-separated RPC endpoints, auto, or none")
	allowlistPath := flag.String("allowlist", "node_allowlist.yaml", "path to Tether node allowlist for --rpc auto")
	apiKey := flag.String("api-key", "", "API key required by non-local clients")
	ctxSize := flag.Int("ctx-size", 8192, "context size per loaded model")
	parallel := flag.Int("parallel", 2, "parallel requests per loaded model")
	idleUnload := flag.Duration("idle-unload", 5*time.Minute, "unload an idle model worker after this duration; 0 disables idle unload")
	modelOverhead := flag.Float64("model-overhead", 1.15, "multiply GGUF file size by this runtime memory reserve")
	kvBytesPerToken := flag.Int64("kv-cache-bytes-per-token", 256*1024, "conservative KV-cache VRAM reserve per context token")
	flag.Parse()

	host, port, err := net.SplitHostPort(*listen)
	if err != nil || host == "" || port == "" {
		log.Fatalf("-listen must be a host:port, for example 127.0.0.1:11435")
	}
	if !isLoopbackHost(host) && *apiKey == "" {
		log.Fatal("-api-key is required when -listen is reachable beyond this machine")
	}
	if *ctxSize < 1 || *parallel < 1 || *idleUnload < 0 || *modelOverhead < 1 || *kvBytesPerToken < 0 {
		log.Fatal("-ctx-size and -parallel must be positive; -idle-unload and -kv-cache-bytes-per-token cannot be negative; -model-overhead must be at least 1")
	}
	if info, err := os.Stat(*modelsDir); err != nil || !info.IsDir() {
		log.Fatalf("model directory %q is unavailable: %v", *modelsDir, err)
	}
	if info, err := os.Stat(*llamaServer); err != nil || info.IsDir() {
		log.Fatalf("llama-server at %q is unavailable: %v", *llamaServer, err)
	}

	gateway, err := newGateway(gatewayConfig{
		modelsDir: *modelsDir, llamaServer: *llamaServer, rpcMode: *rpc, allowlistPath: *allowlistPath,
		apiKey: *apiKey, ctxSize: *ctxSize, parallel: *parallel, idleTimeout: *idleUnload,
		modelOverhead: *modelOverhead, kvBytesPerToken: *kvBytesPerToken,
	})
	if err != nil {
		log.Fatalf("loading model library: %v", err)
	}
	server := &http.Server{Addr: *listen, Handler: gateway, ReadHeaderTimeout: 10 * time.Second}
	serverErr := make(chan error, 1)
	go func() {
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			serverErr <- err
		}
	}()

	log.Printf("Tether API listening on http://%s/v1", *listen)
	log.Printf("OpenAI routes: GET /v1/models, POST /v1/chat/completions")
	if strings.EqualFold(*rpc, "auto") {
		log.Printf("RPC placement: whole-model GPU when it fits; RPC mesh only when required")
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	select {
	case err := <-serverErr:
		log.Fatalf("Tether API stopped: %v", err)
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
		gateway.shutdown(shutdownCtx)
	}
}

// resolveRPCEndpoints handles a manual list, an intentionally local-only
// server, or the Tether registry's currently online Agents. Auto mode is
// conservative: an endpoint is returned only after its pinned Agent reports
// that its local RPC server is Running.
func resolveRPCEndpoints(value, allowlistPath string) ([]string, error) {
	value = strings.TrimSpace(value)
	switch strings.ToLower(value) {
	case "", "none":
		return nil, nil
	case "auto":
		return runningAgentEndpoints(allowlistPath)
	default:
		endpoints := strings.Split(value, ",")
		for i, endpoint := range endpoints {
			endpoint = strings.TrimSpace(endpoint)
			if _, _, err := net.SplitHostPort(endpoint); err != nil {
				return nil, fmt.Errorf("RPC endpoint %q must be host:port: %w", endpoint, err)
			}
			endpoints[i] = endpoint
		}
		return endpoints, nil
	}
}

func runningAgentEndpoints(allowlistPath string) ([]string, error) {
	allowlist, err := registry.LoadAllowlist(allowlistPath)
	if err != nil {
		return nil, err
	}
	peers, err := registry.QueryTailscalePeers()
	if err != nil {
		return nil, err
	}
	identity, err := certs.LoadOrCreate(orchestratorIdentityName)
	if err != nil {
		return nil, err
	}

	nodes := registry.Build(allowlist, peers).Online()
	sort.Slice(nodes, func(i, j int) bool { return nodes[i].Hostname < nodes[j].Hostname })
	endpoints := make([]string, 0, len(nodes))
	for _, node := range nodes {
		tlsConfig, err := trust.PinnedTLSConfig(identity.TLSCertificate(), node.Hostname, false)
		if err != nil {
			log.Printf("Skipping %s: Agent is not paired", node.Hostname)
			continue
		}
		status, err := agent.NewClient(tlsConfig).GetStatus(fmt.Sprintf("%s:%d", node.TailscaleIP, node.AgentPort))
		if err != nil {
			log.Printf("Skipping %s: Agent is unreachable", node.Hostname)
			continue
		}
		if status.Status != "Running" {
			log.Printf("Skipping %s: RPC server is %s", node.Hostname, status.Status)
			continue
		}
		endpoints = append(endpoints, fmt.Sprintf("%s:%d", node.TailscaleIP, node.RPCPort))
	}
	return endpoints, nil
}

func isLoopbackHost(host string) bool {
	host = strings.Trim(host, "[]")
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
