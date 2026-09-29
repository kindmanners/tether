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
	if err := os.WriteFile(configPath, []byte("rpc_server_path: "+binaryPath+"\nrpc_listen_host: 127.0.0.1\n"), 0600); err != nil {
		t.Fatalf("writing test config: %v", err)
	}

	cfg, err := Load(configPath)
	if err != nil {
		t.Fatalf("Load returned an error: %v", err)
	}
	if cfg.RPCServerPath != binaryPath || cfg.RPCListenHost != "127.0.0.1" {
		t.Errorf("Load returned %#v", cfg)
	}
}

func TestLoadRequiresLoopbackRPCListenHost(t *testing.T) {
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
	if err == nil || !strings.Contains(err.Error(), "rpc_listen_host must be 127.0.0.1") {
		t.Fatalf("Load error = %v, want loopback-only rpc_listen_host error", err)
	}
	if err := os.WriteFile(configPath, []byte("rpc_server_path: "+binaryPath+"\nrpc_listen_host: 0.0.0.0\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(configPath); err == nil || !strings.Contains(err.Error(), "rpc_listen_host must be 127.0.0.1") {
		t.Fatalf("Load error = %v, want rejection of wildcard bind", err)
	}
}
