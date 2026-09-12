package agentconfig

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadAcceptsLocalRPCServerAndBindHost(t *testing.T) {
	dir := t.TempDir()
	binaryPath := filepath.Join(dir, "ggml-rpc-server")
	if err := os.WriteFile(binaryPath, nil, 0755); err != nil {
		t.Fatalf("creating test binary: %v", err)
	}
	configPath := filepath.Join(dir, "agent_config.yaml")
	if err := os.WriteFile(configPath, []byte("rpc_server_path: "+binaryPath+"\nrpc_listen_host: 100.64.246.74\n"), 0600); err != nil {
		t.Fatalf("writing test config: %v", err)
	}

	cfg, err := Load(configPath)
	if err != nil {
		t.Fatalf("Load returned an error: %v", err)
	}
	if cfg.RPCServerPath != binaryPath || cfg.RPCListenHost != "100.64.246.74" {
		t.Errorf("Load returned %#v", cfg)
	}
}

func TestLoadRequiresRPCListenHost(t *testing.T) {
	dir := t.TempDir()
	binaryPath := filepath.Join(dir, "ggml-rpc-server")
	if err := os.WriteFile(binaryPath, nil, 0755); err != nil {
		t.Fatalf("creating test binary: %v", err)
	}
	configPath := filepath.Join(dir, "agent_config.yaml")
	if err := os.WriteFile(configPath, []byte("rpc_server_path: "+binaryPath+"\n"), 0600); err != nil {
		t.Fatalf("writing test config: %v", err)
	}

	_, err := Load(configPath)
	if err == nil || !strings.Contains(err.Error(), "rpc_listen_host is required") {
		t.Fatalf("Load error = %v, want missing rpc_listen_host error", err)
	}
}
