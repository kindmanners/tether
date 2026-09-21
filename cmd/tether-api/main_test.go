package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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
