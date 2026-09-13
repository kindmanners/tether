package agent

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	agentconfig "tether/internal/config"
)

func TestHandleCapabilitiesServesBootstrapReport(t *testing.T) {
	configHome := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", configHome)
	dir, err := agentconfig.DefaultDirectory()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	report := `{"observedAt":"2026-09-13T12:00:00Z","hostname":"mathesis","cudaVersion":"13.4","gpus":[{"name":"NVIDIA GPU","driverVersion":"1","vramBytes":4294967296,"vramFreeBytes":3435973837}]}`
	if err := os.WriteFile(filepath.Join(dir, "bootstrap-report.json"), []byte(report), 0o600); err != nil {
		t.Fatal(err)
	}

	request := httptest.NewRequest(http.MethodGet, "/capabilities", nil)
	recorder := httptest.NewRecorder()
	(&Server{}).handleCapabilities(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d: %s", recorder.Code, http.StatusOK, recorder.Body.String())
	}
	if contentType := recorder.Header().Get("Content-Type"); contentType != "application/json" {
		t.Fatalf("content type = %q, want application/json", contentType)
	}
}

func TestHandleCapabilitiesReportsMissingFile(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	request := httptest.NewRequest(http.MethodGet, "/capabilities", nil)
	recorder := httptest.NewRecorder()
	(&Server{}).handleCapabilities(recorder, request)

	if recorder.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusNotFound)
	}
}
