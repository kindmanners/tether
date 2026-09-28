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
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"tether/internal/certs"
	agentconfig "tether/internal/config"
	"tether/internal/process"
	"tether/internal/trust"
)

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
	if host == "127.0.0.1" && port != "" {
		time.Sleep(30 * time.Second)
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
	commandServer := NewServer(&agentconfig.Config{RPCServerPath: executable, RPCListenHost: "127.0.0.1"}, manager)
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

func TestRPCTunnelForwardsOverPinnedMTLS(t *testing.T) {
	configHome := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", configHome)
	t.Setenv("APPDATA", configHome)
	agentIdentity, err := certs.LoadOrCreate("agent-rpc")
	if err != nil {
		t.Fatal(err)
	}
	orchestratorIdentity, err := certs.LoadOrCreate("orchestrator-rpc")
	if err != nil {
		t.Fatal(err)
	}
	if err := trust.Pin("agent-rpc", agentIdentity.CertDER); err != nil {
		t.Fatal(err)
	}
	if err := trust.Pin("orchestrator-rpc", orchestratorIdentity.CertDER); err != nil {
		t.Fatal(err)
	}
	agentTLS, err := trust.PinnedTLSConfig(agentIdentity.TLSCertificate(), "orchestrator-rpc", true)
	if err != nil {
		t.Fatal(err)
	}
	orchestratorTLS, err := trust.PinnedTLSConfig(orchestratorIdentity.TLSCertificate(), "agent-rpc", false)
	if err != nil {
		t.Fatal(err)
	}

	backend, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer backend.Close()
	go func() {
		for {
			conn, acceptErr := backend.Accept()
			if acceptErr != nil {
				return
			}
			go func() { _, _ = io.Copy(conn, conn); _ = conn.Close() }()
		}
	}()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	manager := process.NewManager()
	defer func() { _ = manager.StopDefault() }()
	commandServer := NewServer(&agentconfig.Config{RPCServerPath: executable, RPCListenHost: "127.0.0.1"}, manager)
	server := httptest.NewUnstartedServer(commandServer.handler())
	server.TLS = agentTLS.Clone()
	server.TLS.NextProtos = []string{"http/1.1"}
	server.StartTLS()
	defer server.Close()
	client := NewClient(orchestratorTLS)
	addr := strings.TrimPrefix(server.URL, "https://")
	port := backend.Addr().(*net.TCPAddr).Port
	if _, err := client.StartRPCServer(addr, port); err != nil {
		t.Fatal(err)
	}
	stream, err := client.DialRPC(context.Background(), addr)
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	if _, err := stream.Write([]byte("secure-rpc")); err != nil {
		t.Fatal(err)
	}
	got := make([]byte, len("secure-rpc"))
	if _, err := io.ReadFull(stream, got); err != nil || string(got) != "secure-rpc" {
		t.Fatalf("forwarded bytes = %q, %v", got, err)
	}
	if _, err := client.StopRPCServer(addr); err != nil {
		t.Fatal(err)
	}
	_ = stream.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
	if _, err := stream.Read(make([]byte, 1)); err == nil {
		t.Fatal("RPC stream remained open after managed process stopped")
	}
}

func TestRPCStreamLimitsAreReleased(t *testing.T) {
	server := NewServer(&agentconfig.Config{}, process.NewManager())
	var peer [32]byte
	for range maxRPCStreamsPerPeer {
		if !server.reserveStream(peer) {
			t.Fatal("stream rejected before per-peer limit")
		}
	}
	if server.reserveStream(peer) {
		t.Fatal("stream accepted beyond per-peer limit")
	}
	server.releaseReservation(peer)
	if !server.reserveStream(peer) {
		t.Fatal("released stream capacity was not reusable")
	}

	totalServer := NewServer(&agentconfig.Config{}, process.NewManager())
	for i := range maxRPCStreamsTotal {
		var distinctPeer [32]byte
		distinctPeer[0] = byte(i)
		if !totalServer.reserveStream(distinctPeer) {
			t.Fatalf("stream %d rejected before total limit", i)
		}
	}
	var additionalPeer [32]byte
	additionalPeer[0] = byte(maxRPCStreamsTotal)
	if totalServer.reserveStream(additionalPeer) {
		t.Fatal("stream accepted beyond total Agent limit")
	}
}

func TestRPCReservationDuringShutdownIsReleasedOnce(t *testing.T) {
	server := NewServer(&agentconfig.Config{}, process.NewManager())
	var peer [32]byte
	if !server.reserveStream(peer) {
		t.Fatal("initial stream reservation was rejected")
	}

	server.beginShutdown()
	if server.registerStream(&rpcStream{peer: peer}) {
		t.Fatal("stream registered after shutdown began")
	}
	server.releaseReservation(peer)

	server.streamMu.Lock()
	defer server.streamMu.Unlock()
	if server.totalStreams != 0 || len(server.peerStreams) != 0 {
		t.Fatalf("stream counters after shutdown race = total %d, peers %v", server.totalStreams, server.peerStreams)
	}
}

func TestRPCConnectRejectsCallerControlledDestinations(t *testing.T) {
	server := NewServer(&agentconfig.Config{}, process.NewManager())
	tests := []struct {
		name    string
		target  string
		body    io.Reader
		header  http.Header
		chunked bool
	}{
		{name: "authority form", target: "http://127.0.0.1:9"},
		{name: "query", target: "/rpc?port=9"},
		{name: "body", target: "/rpc", body: bytes.NewBufferString("127.0.0.1:9")},
		{name: "chunked body", target: "/rpc", body: bytes.NewBufferString("127.0.0.1:9"), chunked: true},
		{name: "target header", target: "/rpc", header: http.Header{"X-Tether-Rpc-Target": []string{"127.0.0.1:9"}}},
		{name: "destination header", target: "/rpc", header: http.Header{"X-Tether-Rpc-Destination": []string{"127.0.0.1:9"}}},
		{name: "port header", target: "/rpc", header: http.Header{"X-Tether-Rpc-Port": []string{"9"}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodConnect, test.target, test.body)
			if test.chunked {
				request.ContentLength = -1
				request.TransferEncoding = []string{"chunked"}
			}
			request.Header = test.header
			request.TLS = &tls.ConnectionState{PeerCertificates: []*x509.Certificate{{Raw: []byte("paired-peer")}}}
			recorder := httptest.NewRecorder()
			server.handleRPC(recorder, request)
			if recorder.Code != http.StatusBadRequest {
				t.Fatalf("CONNECT injection returned %d, want %d", recorder.Code, http.StatusBadRequest)
			}
		})
	}
}

func TestAgentRPCServerRefusesHTTP2OnlyALPN(t *testing.T) {
	configHome := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", configHome)
	t.Setenv("APPDATA", configHome)
	agentIdentity, err := certs.LoadOrCreate("agent-http1")
	if err != nil {
		t.Fatal(err)
	}
	orchestratorIdentity, err := certs.LoadOrCreate("orchestrator-http1")
	if err != nil {
		t.Fatal(err)
	}
	if err := trust.Pin("agent-http1", agentIdentity.CertDER); err != nil {
		t.Fatal(err)
	}
	if err := trust.Pin("orchestrator-http1", orchestratorIdentity.CertDER); err != nil {
		t.Fatal(err)
	}
	agentTLS, err := trust.PinnedTLSConfig(agentIdentity.TLSCertificate(), "orchestrator-http1", true)
	if err != nil {
		t.Fatal(err)
	}
	clientTLS, err := trust.PinnedTLSConfig(orchestratorIdentity.TLSCertificate(), "agent-http1", false)
	if err != nil {
		t.Fatal(err)
	}
	clientTLS.NextProtos = []string{"h2"}

	probe, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := probe.Addr().String()
	_ = probe.Close()
	ctx, cancel := context.WithCancel(context.Background())
	serverDone := make(chan error, 1)
	go func() {
		serverDone <- NewServer(&agentconfig.Config{}, process.NewManager()).Start(ctx, addr, agentTLS)
	}()
	deadline := time.Now().Add(2 * time.Second)
	for {
		connection, dialErr := net.DialTimeout("tcp", addr, 50*time.Millisecond)
		if dialErr == nil {
			_ = connection.Close()
			break
		}
		if time.Now().After(deadline) {
			cancel()
			t.Fatalf("Agent server did not start: %v", dialErr)
		}
		time.Sleep(10 * time.Millisecond)
	}

	connection, err := tls.DialWithDialer(&net.Dialer{Timeout: time.Second}, "tcp", addr, clientTLS)
	if connection != nil {
		_ = connection.Close()
	}
	if err == nil {
		cancel()
		t.Fatal("Agent negotiated an HTTP/2-only RPC connection")
	}
	cancel()
	select {
	case err := <-serverDone:
		if err != nil {
			t.Fatalf("Agent shutdown returned %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Agent server did not shut down")
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
