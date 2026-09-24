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
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"tether/internal/gguf"
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

func TestScanGatewayModelsGroupsSplitFilesAndSkipsProjectors(t *testing.T) {
	dir := t.TempDir()
	files := map[string]int{
		"large-00001-of-00003.gguf": 3,
		"large-00002-of-00003.gguf": 4,
		"large-00003-of-00003.gguf": 5,
		"mmproj-large.gguf":         7,
	}
	for name, size := range files {
		if err := os.WriteFile(filepath.Join(dir, name), make([]byte, size), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	models, err := scanGatewayModels(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 1 || models["large"] == "" {
		t.Fatalf("unexpected split model library: %#v", models)
	}
	if size, err := gguf.Size(models["large"]); err != nil || size != 12 {
		t.Fatalf("split model size = %d, %v; want 12", size, err)
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
				req.Host = "localhost"
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
			req.Host = "localhost"
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
	gateway, err := newGateway(gatewayConfig{modelsDir: dir, apiKey: "test-api-key", requestShutdown: func() { called <- struct{}{} }})
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
		req.Host = "localhost"
		req.RemoteAddr = test.remote
		req.Header.Set("Authorization", "Bearer test-api-key")
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
	gateway, err := newGateway(gatewayConfig{modelsDir: dir, apiKey: "test-api-key"})
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
		req.Header.Set("Authorization", "Bearer test-api-key")
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
	err := waitForWorker(context.Background(), strings.TrimPrefix(server.URL, "http://"), &modelWorker{done: make(chan struct{})}, 120*time.Millisecond)
	if err == nil {
		t.Fatal("waitForWorker succeeded for a stalled health endpoint")
	}
	if elapsed := time.Since(start); elapsed > 250*time.Millisecond {
		t.Fatalf("waitForWorker exceeded bounded startup timeout: %s", elapsed)
	}
}

func TestWaitForWorkerDetectsProcessExit(t *testing.T) {
	worker := &modelWorker{done: make(chan struct{}), exitErr: fmt.Errorf("CUDA out of memory")}
	close(worker.done)
	start := time.Now()
	err := waitForWorker(context.Background(), "127.0.0.1:1", worker, 5*time.Second)
	if err == nil || !strings.Contains(err.Error(), "CUDA out of memory") {
		t.Fatalf("waitForWorker error = %v", err)
	}
	if elapsed := time.Since(start); elapsed > 500*time.Millisecond {
		t.Fatalf("process exit detection took %s", elapsed)
	}
}

func TestConcurrentAcquireLaunchesOneWorker(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "model.gguf"), []byte("GGUF"), 0o600); err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	launches := 0
	gateway, err := newGateway(gatewayConfig{
		modelsDir: dir,
		apiKey:    "test-api-key",
		planner: func(string) (placement.Plan, error) {
			return placement.Plan{Mode: "whole", Nodes: []placement.Node{{Hostname: "node-a"}}}, nil
		},
		launcher: func(_ context.Context, modelID, modelPath string, plan placement.Plan) (*modelWorker, error) {
			mu.Lock()
			launches++
			mu.Unlock()
			time.Sleep(50 * time.Millisecond)
			return &modelWorker{modelID: modelID, model: modelPath, plan: plan, done: make(chan struct{})}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	workers := make(chan *modelWorker, 2)
	errs := make(chan error, 2)
	for range 2 {
		go func() {
			worker, acquireErr := gateway.acquire(context.Background(), "model")
			workers <- worker
			errs <- acquireErr
		}()
	}
	first, second := <-workers, <-workers
	if err := <-errs; err != nil {
		t.Fatal(err)
	}
	if err := <-errs; err != nil {
		t.Fatal(err)
	}
	if first != second || launches != 1 {
		t.Fatalf("workers = %p, %p; launches = %d, want one shared launch", first, second, launches)
	}
}

func TestWatchWorkerRemovesCrashedWorker(t *testing.T) {
	gateway := &gateway{models: map[string]string{"model": "model.gguf"}, workers: make(map[string]*modelWorker), states: make(map[string]modelState)}
	worker := &modelWorker{key: "model\x00node", modelID: "model", state: "loaded", done: make(chan struct{}), exitErr: fmt.Errorf("worker failed")}
	gateway.workers[worker.key] = worker
	done := make(chan struct{})
	go func() {
		gateway.watchWorker(worker)
		close(done)
	}()
	close(worker.done)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("worker watcher did not finish")
	}
	if gateway.workerForModelLocked("model") != nil || gateway.states["model"].State != "crashed" {
		t.Fatalf("crashed worker was not removed: workers=%#v state=%#v", gateway.workers, gateway.states["model"])
	}
}

func TestChatProxyFlushesStreamingAndScrubsCredentials(t *testing.T) {
	checkedHeaders := make(chan error, 1)
	release := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer worker-secret" {
			checkedHeaders <- fmt.Errorf("worker authorization = %q", got)
			return
		}
		if got := r.Header.Get("Cookie"); got != "" {
			checkedHeaders <- fmt.Errorf("worker received cookie %q", got)
			return
		}
		checkedHeaders <- nil
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: first\n\n")
		w.(http.Flusher).Flush()
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	defer upstream.Close()
	address := strings.TrimPrefix(upstream.URL, "http://")
	worker := &modelWorker{key: "model\x00node", modelID: "model", address: address, apiKey: "worker-secret", state: "loaded", done: make(chan struct{})}
	gateway := &gateway{
		cfg:     gatewayConfig{apiKey: "gateway-secret"},
		models:  map[string]string{"model": "unused.gguf"},
		workers: map[string]*modelWorker{worker.key: worker},
		states:  make(map[string]modelState),
	}
	server := httptest.NewServer(gateway)
	defer server.Close()
	request, err := http.NewRequest(http.MethodPost, server.URL+"/v1/chat/completions", bytes.NewBufferString(`{"model":"model","stream":true}`))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer gateway-secret")
	request.Header.Set("Cookie", "browser-session=secret")
	start := time.Now()
	response, err := server.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	line, err := bufio.NewReader(response.Body).ReadString('\n')
	if err != nil || line != "data: first\n" {
		t.Fatalf("first SSE line = %q, %v", line, err)
	}
	if elapsed := time.Since(start); elapsed > 300*time.Millisecond {
		t.Fatalf("first SSE event was buffered for %s", elapsed)
	}
	close(release)
	if err := <-checkedHeaders; err != nil {
		t.Fatal(err)
	}
}

func TestModelPlanRouteIncludesCurrentReservation(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "model.gguf"), []byte("GGUF"), 0o600); err != nil {
		t.Fatal(err)
	}
	gateway, err := newGateway(gatewayConfig{modelsDir: dir, apiKey: "test-api-key", planner: func(string) (placement.Plan, error) {
		return placement.Plan{Mode: "whole", Nodes: []placement.Node{{Hostname: "node-a"}}, Requirement: placement.Requirement{ModelBytes: 100, KVCacheBytes: 20}}, nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/v1/models/model/plan", nil)
	request.Host = "localhost"
	request.Header.Set("Authorization", "Bearer test-api-key")
	gateway.ServeHTTP(recorder, request)
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
	gateway, err := newGateway(gatewayConfig{modelsDir: dir, apiKey: "test-api-key"})
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
