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

package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"tether/internal/certs"
	agentconfig "tether/internal/config"
	"tether/internal/process"
	"tether/internal/trust"
)

const agentE2EHelperHost = "__agent_e2e_test_sleep__"

func TestMain(m *testing.M) {
	var host, port string
	for i := 1; i+1 < len(os.Args); i++ {
		switch os.Args[i] {
		case "--host":
			host = os.Args[i+1]
		case "--port":
			port = os.Args[i+1]
		}
	}
	if host == agentE2EHelperHost {
		seconds, _ := strconv.Atoi(port)
		time.Sleep(time.Duration(seconds) * time.Second)
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func TestAgentClientServerEndToEndOverPinnedMTLS(t *testing.T) {
	configHome := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", configHome)
	t.Setenv("APPDATA", configHome)
	agentIdentity, err := certs.LoadOrCreate("agent-e2e")
	if err != nil {
		t.Fatal(err)
	}
	orchestratorIdentity, err := certs.LoadOrCreate("orchestrator-e2e")
	if err != nil {
		t.Fatal(err)
	}
	if err := trust.Pin("agent-e2e", agentIdentity.CertDER); err != nil {
		t.Fatal(err)
	}
	if err := trust.Pin("orchestrator-e2e", orchestratorIdentity.CertDER); err != nil {
		t.Fatal(err)
	}
	agentTLS, err := trust.PinnedTLSConfig(agentIdentity.TLSCertificate(), "orchestrator-e2e", true)
	if err != nil {
		t.Fatal(err)
	}
	orchestratorTLS, err := trust.PinnedTLSConfig(orchestratorIdentity.TLSCertificate(), "agent-e2e", false)
	if err != nil {
		t.Fatal(err)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(fmt.Errorf("locating test executable: %w", err))
	}
	manager := process.NewManager()
	defer func() { _ = manager.StopDefault() }()
	commandServer := NewServer(&agentconfig.Config{RPCServerPath: executable, RPCListenHost: agentE2EHelperHost}, manager)
	server := httptest.NewUnstartedServer(commandServer.handler())
	server.TLS = agentTLS.Clone()
	server.StartTLS()
	defer server.Close()
	client := NewClient(orchestratorTLS)
	addr := strings.TrimPrefix(server.URL, "https://")

	status, err := client.GetStatus(addr)
	if err != nil || status.Status != "Stopped" {
		t.Fatalf("initial GetStatus() = %#v, %v", status, err)
	}
	status, err = client.StartRPCServer(addr, 30)
	if err != nil || status.Status != "Running" {
		t.Fatalf("StartRPCServer() = %#v, %v", status, err)
	}
	status, err = client.StopRPCServer(addr)
	if err != nil || status.Status != "Stopped" {
		t.Fatalf("StopRPCServer() = %#v, %v", status, err)
	}
}

func TestClientStatusCapabilitiesAndErrors(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/status":
			_, _ = w.Write([]byte(`{"status":"Running","last_error":""}`))
		case "/capabilities":
			_, _ = w.Write([]byte(`{"hostname":"node","gpus":[{"name":"GPU","vramFreeBytes":1024}]}`))
		case "/stop":
			writeError(w, http.StatusConflict, "cannot stop")
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	client := &Client{httpClient: server.Client()}
	addr := strings.TrimPrefix(server.URL, "https://")
	status, err := client.GetStatus(addr)
	if err != nil || status.Status != "Running" {
		t.Fatalf("GetStatus() = %#v, %v", status, err)
	}
	capabilities, err := client.GetCapabilities(addr)
	if err != nil || len(capabilities.GPUs) != 1 || capabilities.GPUs[0].VRAMFreeBytes != 1024 {
		t.Fatalf("GetCapabilities() = %#v, %v", capabilities, err)
	}
	if _, err := client.StopRPCServer(addr); err == nil || !strings.Contains(err.Error(), "cannot stop") {
		t.Fatalf("StopRPCServer() error = %v", err)
	}
}

func TestClientContextCancelsAgentRequest(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer server.Close()
	client := &Client{httpClient: server.Client()}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	_, err := client.GetStatusContext(ctx, strings.TrimPrefix(server.URL, "https://"))
	if err == nil || !strings.Contains(err.Error(), "context deadline exceeded") {
		t.Fatalf("GetStatusContext() error = %v", err)
	}
}

func TestCommandHandlersRejectInvalidMethodsAndPorts(t *testing.T) {
	server := NewServer(&agentconfig.Config{}, process.NewManager())
	tests := []struct {
		name    string
		handler http.HandlerFunc
		method  string
		body    string
		want    int
	}{
		{name: "start method", handler: server.handleStart, method: http.MethodGet, want: http.StatusMethodNotAllowed},
		{name: "start malformed", handler: server.handleStart, method: http.MethodPost, body: "{", want: http.StatusBadRequest},
		{name: "start port", handler: server.handleStart, method: http.MethodPost, body: `{"port":0}`, want: http.StatusBadRequest},
		{name: "stop method", handler: server.handleStop, method: http.MethodGet, want: http.StatusMethodNotAllowed},
		{name: "status method", handler: server.handleStatus, method: http.MethodPost, want: http.StatusMethodNotAllowed},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(test.method, "/", strings.NewReader(test.body))
			recorder := httptest.NewRecorder()
			test.handler(recorder, request)
			if recorder.Code != test.want {
				t.Fatalf("status = %d, want %d: %s", recorder.Code, test.want, recorder.Body.String())
			}
		})
	}
}

func TestHandleCapabilitiesServesBootstrapReport(t *testing.T) {
	configHome := t.TempDir()
	emptyPath := t.TempDir()
	// os.UserConfigDir uses APPDATA on Windows and XDG_CONFIG_HOME on Linux.
	// Set both so this test never reads or writes the developer's local state.
	t.Setenv("APPDATA", configHome)
	t.Setenv("XDG_CONFIG_HOME", configHome)
	// Keep this fixture deterministic when the test runner itself has NVIDIA
	// tooling installed: the handler otherwise deliberately replaces the
	// bootstrapped values with current hardware telemetry.
	t.Setenv("PATH", emptyPath)
	dir, err := agentconfig.DefaultDirectory()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	report := `{"observedAt":"2026-09-13T12:00:00Z","hostname":"mathesis","cudaVersion":"13.4","gpus":[{"name":"NVIDIA GPU","driverVersion":"1","vramBytes":4294967296,"vramFreeBytes":3435973837,"utilizationPercent":38}]}`
	if err := os.WriteFile(filepath.Join(dir, "bootstrap-report.json"), append([]byte{0xEF, 0xBB, 0xBF}, []byte(report)...), 0o600); err != nil {
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
	var got CapabilitiesResult
	if err := json.Unmarshal(recorder.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.GPUs[0].UtilizationPercent != 38 {
		t.Fatalf("GPU utilization = %d, want 38", got.GPUs[0].UtilizationPercent)
	}
}

func TestHandleCapabilitiesReportsMissingFile(t *testing.T) {
	configHome := t.TempDir()
	t.Setenv("APPDATA", configHome)
	t.Setenv("XDG_CONFIG_HOME", configHome)
	request := httptest.NewRequest(http.MethodGet, "/capabilities", nil)
	recorder := httptest.NewRecorder()
	(&Server{}).handleCapabilities(recorder, request)

	if recorder.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusNotFound)
	}
}
