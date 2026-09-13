package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestScanGGUFModels(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"one.gguf", "nested/two.GGUF", "notes.txt"} {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("test"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	models, err := scanGGUFModels(dir)
	if err != nil {
		t.Fatalf("scanGGUFModels returned an error: %v", err)
	}
	if len(models) != 2 {
		t.Fatalf("got %d models, want 2", len(models))
	}
	if models[0].Path != "~/models/nested/two.GGUF" || models[1].Path != "~/models/one.gguf" {
		t.Fatalf("unexpected models: %#v", models)
	}
}

func TestFetchModelStates(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/model-states" {
			t.Fatalf("unexpected path %q", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"models":[{"model":"one","state":"idle-countdown","nodes":["mathesis"]}]}`))
	}))
	defer server.Close()
	states, err := fetchModelStates(server.URL + "/api/v1/model-states")
	if err != nil {
		t.Fatal(err)
	}
	state, ok := states["one"]
	if !ok || state.State != "idle-countdown" || len(state.Nodes) != 1 || state.Nodes[0] != "mathesis" {
		t.Fatalf("unexpected model states: %#v", states)
	}
}

func TestScanGGUFModelsMissingDirectory(t *testing.T) {
	models, err := scanGGUFModels(filepath.Join(t.TempDir(), "missing"))
	if err != nil {
		t.Fatalf("scanGGUFModels returned an error: %v", err)
	}
	if len(models) != 0 {
		t.Fatalf("got %d models, want no models", len(models))
	}
}

func TestParseNvidiaSMI(t *testing.T) {
	gpus, err := parseNvidiaSMI("NVIDIA GeForce RTX 3060, 12288, 9630, 555.42\n")
	if err != nil {
		t.Fatalf("parseNvidiaSMI returned an error: %v", err)
	}
	if len(gpus) != 1 {
		t.Fatalf("got %d GPUs, want 1", len(gpus))
	}
	if gpus[0].VRAMBytes != 12288*1024*1024 || gpus[0].VRAMFreeBytes != 9630*1024*1024 {
		t.Fatalf("unexpected VRAM values: %#v", gpus[0])
	}
}

func TestParseNvidiaSMIRejectsMalformedRows(t *testing.T) {
	if _, err := parseNvidiaSMI("not a GPU row"); err == nil {
		t.Fatal("parseNvidiaSMI succeeded for a malformed row")
	}
}
