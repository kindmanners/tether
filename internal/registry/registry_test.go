// Copyright (C) 2026 kindmanners on github
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.

package registry

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLoadAllowlistAndBuildRegistry(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nodes.yaml")
	yaml := "nodes:\n  - hostname: alpha\n    role: rpc-node\n    agent_port: 7420\n    rpc_port: 50052\n  - hostname: beta\n    role: rpc-node\n    agent_port: 7421\n    rpc_port: 50053\n"
	if err := os.WriteFile(path, []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}
	allowlist, err := LoadAllowlist(path)
	if err != nil {
		t.Fatal(err)
	}
	seen := time.Now().UTC().Truncate(time.Second)
	reg := Build(allowlist, []LivePeer{
		{Hostname: "alpha", IPv4Address: "100.64.0.1", Online: true, SeenAt: seen},
		{Hostname: "unlisted", IPv4Address: "100.64.0.9", Online: true, SeenAt: seen},
	})
	alpha, ok := reg.Get("alpha")
	if !ok || alpha.Status != StatusOnline || alpha.TailscaleIP != "100.64.0.1" || !alpha.LastSeen.Equal(seen) {
		t.Fatalf("alpha = %#v, found %v", alpha, ok)
	}
	beta, ok := reg.Get("beta")
	if !ok || beta.Status != StatusOffline {
		t.Fatalf("beta = %#v, found %v", beta, ok)
	}
	if _, ok := reg.Get("unlisted"); ok {
		t.Fatal("peer-only node entered registry")
	}
	if got := reg.Online(); len(got) != 1 || got[0].Hostname != "alpha" {
		t.Fatalf("Online() = %#v", got)
	}
	if got := reg.All(); len(got) != 2 {
		t.Fatalf("All() returned %d nodes, want 2", len(got))
	}
}

func TestLoadAllowlistRejectsInvalidEntries(t *testing.T) {
	tests := map[string]string{
		"malformed":        "nodes: [",
		"missing hostname": "nodes:\n  - role: rpc-node\n    agent_port: 1\n    rpc_port: 2\n",
		"duplicate":        "nodes:\n  - hostname: alpha\n    role: rpc\n    agent_port: 1\n    rpc_port: 2\n  - hostname: alpha\n    role: rpc\n    agent_port: 3\n    rpc_port: 4\n",
		"same ports":       "nodes:\n  - hostname: alpha\n    role: rpc\n    agent_port: 7420\n    rpc_port: 7420\n",
		"invalid port":     "nodes:\n  - hostname: alpha\n    role: rpc\n    agent_port: 0\n    rpc_port: 2\n",
	}
	for name, contents := range tests {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "nodes.yaml")
			if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := LoadAllowlist(path); err == nil {
				t.Fatal("LoadAllowlist() succeeded")
			}
		})
	}
}

func TestTailscaleValueHelpers(t *testing.T) {
	if got := shortHostname("alpha.example.ts.net."); got != "alpha" {
		t.Fatalf("shortHostname() = %q", got)
	}
	if got := shortHostname("."); got != "" {
		t.Fatalf("shortHostname(.) = %q", got)
	}
	if got := firstIPv4([]string{"fd7a:115c:a1e0::1", "100.64.0.2"}); got != "100.64.0.2" {
		t.Fatalf("firstIPv4() = %q", got)
	}
}
