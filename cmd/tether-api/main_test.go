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
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

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

func TestDefaultLlamaServerPathMatchesPlatform(t *testing.T) {
	got := defaultLlamaServerPath(true)
	want := filepath.Join("llama.cpp", "build-rpc-cuda", "bin", "llama-server")
	if runtime.GOOS == "windows" {
		want = filepath.Join("llama.cpp", "build-rpc-cuda", "bin", "Release", "llama-server.exe")
	}
	if got != want {
		t.Fatalf("defaultLlamaServerPath() = %q, want %q", got, want)
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

func TestLocalOnlyPlacementRejectsOptedOutOrchestrator(t *testing.T) {
	model := filepath.Join(t.TempDir(), "model.gguf")
	if err := os.WriteFile(model, []byte("GGUF"), 0o600); err != nil {
		t.Fatal(err)
	}
	gateway := &gateway{cfg: gatewayConfig{
		rpcMode: "none", modelOverhead: 1, ctxSize: 1, kvBytesPerToken: 0, localGPU: false,
	}}
	if _, err := gateway.planFor(model); err == nil {
		t.Fatal("local-only placement succeeded after the Orchestrator opted out of local GPU contribution")
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

func TestModelManagementRoutesRequireAPIKey(t *testing.T) {
	dir := t.TempDir()
	gateway, err := newGateway(gatewayConfig{modelsDir: dir, apiKey: "test-api-key"})
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name        string
		method      string
		path        string
		validStatus int
	}{
		{name: "states", method: http.MethodGet, path: "/api/v1/model-states", validStatus: http.StatusOK},
		{name: "refresh", method: http.MethodPost, path: "/api/v1/models/refresh", validStatus: http.StatusOK},
		{name: "plan", method: http.MethodGet, path: "/api/v1/models/example/plan", validStatus: http.StatusServiceUnavailable},
		{name: "load", method: http.MethodPost, path: "/api/v1/models/example/load", validStatus: http.StatusServiceUnavailable},
		{name: "unload", method: http.MethodPost, path: "/api/v1/models/example/unload", validStatus: http.StatusServiceUnavailable},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			for _, authorization := range []string{"", "Bearer wrong-key"} {
				req := httptest.NewRequest(test.method, test.path, nil)
				if authorization != "" {
					req.Header.Set("Authorization", authorization)
				}
				recorder := httptest.NewRecorder()
				gateway.ServeHTTP(recorder, req)
				if recorder.Code != http.StatusUnauthorized {
					t.Fatalf("authorization %q returned %d, want %d", authorization, recorder.Code, http.StatusUnauthorized)
				}
				if got := recorder.Header().Get("WWW-Authenticate"); got != "Bearer" {
					t.Fatalf("authorization %q WWW-Authenticate = %q, want Bearer", authorization, got)
				}
			}

			req := httptest.NewRequest(test.method, test.path, nil)
			req.Header.Set("Authorization", "Bearer test-api-key")
			recorder := httptest.NewRecorder()
			gateway.ServeHTTP(recorder, req)
			if recorder.Code != test.validStatus {
				t.Fatalf("valid API key returned %d, want %d", recorder.Code, test.validStatus)
			}
		})
	}
}

func TestLocalShutdownRouteRequiresLoopbackAndSignalsGateway(t *testing.T) {
	dir := t.TempDir()
	called := make(chan struct{}, 1)
	gateway, err := newGateway(gatewayConfig{modelsDir: dir, requestShutdown: func() { called <- struct{}{} }})
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		remote string
		want   int
	}{
		{remote: "100.64.0.1:50000", want: http.StatusForbidden},
		{remote: "127.0.0.1:50000", want: http.StatusAccepted},
	} {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/internal/shutdown", nil)
		req.RemoteAddr = test.remote
		recorder := httptest.NewRecorder()
		gateway.ServeHTTP(recorder, req)
		if recorder.Code != test.want {
			t.Fatalf("remote %s returned %d, want %d", test.remote, recorder.Code, test.want)
		}
	}
	select {
	case <-called:
	case <-time.After(time.Second):
		t.Fatal("loopback shutdown did not notify the gateway")
	}
}

func TestRefreshAndListModelsAreConcurrentSafe(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "model.gguf"), []byte("GGUF"), 0o600); err != nil {
		t.Fatal(err)
	}
	gateway, err := newGateway(gatewayConfig{modelsDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(gateway)
	defer server.Close()

	const requests = 100
	var group sync.WaitGroup
	errs := make(chan error, 2*requests)
	client := server.Client()
	request := func(method, path string) {
		defer group.Done()
		req, err := http.NewRequest(method, server.URL+path, nil)
		if err != nil {
			errs <- err
			return
		}
		response, err := client.Do(req)
		if err != nil {
			errs <- err
			return
		}
		defer response.Body.Close()
		if response.StatusCode != http.StatusOK {
			errs <- fmt.Errorf("%s %s returned status %d, want %d", method, path, response.StatusCode, http.StatusOK)
		}
	}
	for range requests {
		group.Add(2)
		go request(http.MethodGet, "/v1/models")
		go request(http.MethodPost, "/api/v1/models/refresh")
	}
	group.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
}

func TestModelStatesRetainRemovedActiveWorker(t *testing.T) {
	gateway := &gateway{
		models:  map[string]string{},
		workers: map[string]*modelWorker{"removed\x00node": {key: "removed\x00node", modelID: "removed"}},
		states:  map[string]modelState{"removed": {Model: "removed", State: "loaded", Nodes: []string{"node"}}},
	}
	recorder := httptest.NewRecorder()
	gateway.writeStates(recorder)
	if recorder.Code != http.StatusOK {
		t.Fatalf("model states returned %d", recorder.Code)
	}
	var payload struct {
		Models []modelState `json:"models"`
	}
	if err := json.NewDecoder(recorder.Body).Decode(&payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Models) != 1 || payload.Models[0].Model != "removed" || payload.Models[0].State != "removed" {
		t.Fatalf("removed active worker was not exposed for unload: %#v", payload.Models)
	}
}

func TestWaitForWorkerBoundsSlowHealthProbe(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(time.Second)
	}))
	defer server.Close()
	start := time.Now()
	err := waitForWorker(strings.TrimPrefix(server.URL, "http://"), &exec.Cmd{}, 120*time.Millisecond)
	if err == nil {
		t.Fatal("waitForWorker succeeded for a stalled health endpoint")
	}
	if elapsed := time.Since(start); elapsed > 250*time.Millisecond {
		t.Fatalf("waitForWorker exceeded bounded startup timeout: %s", elapsed)
	}
}

func TestModelPlanRouteIncludesCurrentReservation(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "model.gguf"), []byte("GGUF"), 0o600); err != nil {
		t.Fatal(err)
	}
	gateway, err := newGateway(gatewayConfig{modelsDir: dir, planner: func(string) (placement.Plan, error) {
		return placement.Plan{Mode: "whole", Nodes: []placement.Node{{Hostname: "node-a"}}, Requirement: placement.Requirement{ModelBytes: 100, KVCacheBytes: 20}}, nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	recorder := httptest.NewRecorder()
	gateway.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/models/model/plan", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("plan route returned %d: %s", recorder.Code, recorder.Body.String())
	}
	var plan modelPlan
	if err := json.NewDecoder(recorder.Body).Decode(&plan); err != nil {
		t.Fatal(err)
	}
	if plan.ReserveBytes != 120 || plan.ModelReserveBytes != 100 || plan.KVCacheBytes != 20 || plan.ObservedAt == "" {
		t.Fatalf("unexpected placement preview: %#v", plan)
	}
}

func TestRefreshRestoresWorkerStateWhenModelReturns(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "model.gguf")
	if err := os.WriteFile(path, []byte("GGUF"), 0o600); err != nil {
		t.Fatal(err)
	}
	gateway, err := newGateway(gatewayConfig{modelsDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	worker := &modelWorker{key: "model\x00node", modelID: "model", state: "loaded", plan: placement.Plan{Nodes: []placement.Node{{Hostname: "node"}}}}
	gateway.workers[worker.key] = worker
	gateway.states[worker.modelID] = modelState{Model: worker.modelID, State: "loaded"}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := gateway.refreshModels(); err != nil {
		t.Fatal(err)
	}
	if got := gateway.states[worker.modelID].State; got != "removed" {
		t.Fatalf("state after removal = %q, want removed", got)
	}
	if err := os.WriteFile(path, []byte("GGUF"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := gateway.refreshModels(); err != nil {
		t.Fatal(err)
	}
	if got := gateway.states[worker.modelID].State; got != "loaded" {
		t.Fatalf("state after restore = %q, want loaded", got)
	}
}
