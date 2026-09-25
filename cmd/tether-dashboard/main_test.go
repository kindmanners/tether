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

package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

func TestEmbeddedDashboardAssetsAreServed(t *testing.T) {
	handler, err := newDashboardHandler(&dashboardServer{}, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/", "/app.js", "/styles.css"} {
		request := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:8080"+path, nil)
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusOK {
			t.Errorf("GET %s status = %d, want 200", path, recorder.Code)
		}
		if recorder.Body.Len() == 0 {
			t.Errorf("GET %s returned an empty body", path)
		}
	}
}

func TestValidateDashboardListenRequiresLoopback(t *testing.T) {
	for _, address := range []string{"127.0.0.1:8080", "localhost:8080", "[::1]:8080"} {
		if err := validateDashboardListen(address); err != nil {
			t.Errorf("validateDashboardListen(%q) = %v", address, err)
		}
	}
	for _, address := range []string{"0.0.0.0:8080", "[::]:8080", ":8080", "192.0.2.10:8080", "bad-address"} {
		if err := validateDashboardListen(address); err == nil {
			t.Errorf("validateDashboardListen(%q) succeeded, want rejection", address)
		}
	}
}

func TestDashboardRequestPolicyRejectsNonLoopbackHostAndCrossOrigin(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	handler := dashboardRequestPolicy(next)
	tests := []struct {
		name   string
		host   string
		origin string
		want   int
	}{
		{name: "loopback", host: "127.0.0.1:8080", want: http.StatusNoContent},
		{name: "matching origin", host: "localhost:8080", origin: "http://localhost:8080", want: http.StatusNoContent},
		{name: "remote host", host: "cluster.example:8080", want: http.StatusForbidden},
		{name: "cross origin", host: "localhost:8080", origin: "https://attacker.example", want: http.StatusForbidden},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, "http://"+test.host+"/", nil)
			request.Host = test.host
			request.Header.Set("Origin", test.origin)
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, request)
			if recorder.Code != test.want {
				t.Fatalf("status = %d, want %d", recorder.Code, test.want)
			}
		})
	}
}

func TestDashboardCollectionErrorDoesNotExposeInternalPath(t *testing.T) {
	sentinel := filepath.Join(t.TempDir(), "sensitive-allowlist-name.yaml")
	server := &dashboardServer{allowlistPath: sentinel}
	request := httptest.NewRequest(http.MethodGet, "/api/v1/dashboard", nil)
	recorder := httptest.NewRecorder()
	server.handleDashboard(recorder, request)
	body, err := io.ReadAll(recorder.Result().Body)
	if err != nil {
		t.Fatal(err)
	}
	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", recorder.Code)
	}
	if strings.Contains(string(body), sentinel) {
		t.Fatalf("response exposed internal path %q: %s", sentinel, body)
	}
}

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
	authorization := make(chan string, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/model-states" {
			t.Fatalf("unexpected path %q", r.URL.Path)
		}
		authorization <- r.Header.Get("Authorization")
		_, _ = w.Write([]byte(`{"models":[{"model":"one","state":"idle-countdown","nodes":["mathesis"]}]}`))
	}))
	defer server.Close()
	states, err := fetchModelStates(server.URL+"/api/v1/model-states", "dashboard-secret")
	if err != nil {
		t.Fatal(err)
	}
	if got := <-authorization; got != "Bearer dashboard-secret" {
		t.Fatalf("Authorization = %q, want Bearer token", got)
	}
	state, ok := states["one"]
	if !ok || state.State != "idle-countdown" || len(state.Nodes) != 1 || state.Nodes[0] != "mathesis" {
		t.Fatalf("unexpected model states: %#v", states)
	}
}

func TestFetchModelStatesRequiresAPIKeyWithoutRequestingEndpoint(t *testing.T) {
	var requested atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		requested.Store(true)
	}))
	defer server.Close()

	_, err := fetchModelStates(server.URL, "")
	if err == nil || !strings.Contains(err.Error(), "TETHER_API_KEY") {
		t.Fatalf("fetchModelStates error = %v, want missing API key guidance", err)
	}
	if requested.Load() {
		t.Fatal("model-state endpoint was requested without an API key")
	}
}

func TestFetchModelStatesCanBeDisabledWithoutAPIKey(t *testing.T) {
	states, err := fetchModelStates(" ", "")
	if err != nil {
		t.Fatalf("fetchModelStates error = %v, want disabled endpoint to be ignored", err)
	}
	if states != nil {
		t.Fatalf("fetchModelStates states = %#v, want nil", states)
	}
}

func TestFetchModelStatesReportsEndpointStatus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
	}))
	defer server.Close()

	_, err := fetchModelStates(server.URL, "wrong-secret")
	if err == nil || !strings.Contains(err.Error(), "401 Unauthorized") {
		t.Fatalf("fetchModelStates error = %v, want endpoint status", err)
	}
}

func TestSortDashboardNodesPutsOrchestratorFirst(t *testing.T) {
	nodes := []dashboardNode{
		{Hostname: "mathesis"},
		{Hostname: "ataraxia", IsOrchestrator: true},
		{Hostname: "another-node"},
	}
	sortDashboardNodes(nodes)
	if !nodes[0].IsOrchestrator || nodes[0].Hostname != "ataraxia" {
		t.Fatalf("orchestrator was not first: %#v", nodes)
	}
	if nodes[1].Hostname != "another-node" || nodes[2].Hostname != "mathesis" {
		t.Fatalf("non-orchestrator ordering is not deterministic: %#v", nodes)
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
