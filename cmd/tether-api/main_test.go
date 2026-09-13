package main

import (
	"os"
	"path/filepath"
	"testing"

	"tether/internal/placement"
)

func TestResolveRPCEndpointsManual(t *testing.T) {
	endpoints, err := resolveRPCEndpoints("100.64.0.1:50053, [fd7a:115c:a1e0::1]:50053", "unused")
	if err != nil {
		t.Fatalf("resolveRPCEndpoints returned an error: %v", err)
	}
	if len(endpoints) != 2 || endpoints[0] != "100.64.0.1:50053" {
		t.Fatalf("unexpected endpoints: %#v", endpoints)
	}
}

func TestWorkerKeyIncludesPlacement(t *testing.T) {
	first := workerKey("model", placement.Plan{Nodes: []placement.Node{{Hostname: "ataraxia"}}})
	second := workerKey("model", placement.Plan{Nodes: []placement.Node{{Hostname: "mathesis"}}})
	if first == second {
		t.Fatalf("worker keys must differ by selected node: %q", first)
	}
}

func TestResolveRPCEndpointsLocalOnly(t *testing.T) {
	endpoints, err := resolveRPCEndpoints("none", "unused")
	if err != nil {
		t.Fatalf("resolveRPCEndpoints returned an error: %v", err)
	}
	if len(endpoints) != 0 {
		t.Fatalf("got endpoints %#v, want none", endpoints)
	}
}

func TestResolveRPCEndpointsRejectsBadEndpoint(t *testing.T) {
	if _, err := resolveRPCEndpoints("not-an-endpoint", "unused"); err == nil {
		t.Fatal("resolveRPCEndpoints succeeded for an invalid endpoint")
	}
}

func TestIsLoopbackHost(t *testing.T) {
	for _, host := range []string{"127.0.0.1", "localhost", "[::1]"} {
		if !isLoopbackHost(host) {
			t.Fatalf("isLoopbackHost(%q) = false, want true", host)
		}
	}
	if isLoopbackHost("0.0.0.0") {
		t.Fatal("isLoopbackHost(0.0.0.0) = true, want false")
	}
}

func TestScanGatewayModels(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "one.gguf"), []byte("GGUF"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "nested"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "nested", "two.GGUF"), []byte("GGUF"), 0o600); err != nil {
		t.Fatal(err)
	}
	models, err := scanGatewayModels(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 2 || models["one"] == "" || models["two"] == "" {
		t.Fatalf("unexpected model library: %#v", models)
	}
}
