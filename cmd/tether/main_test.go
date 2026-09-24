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
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"tether/internal/agent"
	"tether/internal/registry"
)

func TestPlatformLlamaServerPath(t *testing.T) {
	got := platformLlamaServerPath(filepath.Join("root", "build-rpc"))
	want := filepath.Join("root", "build-rpc", "bin", "llama-server")
	if runtime.GOOS == "windows" {
		want = filepath.Join("root", "build-rpc", "bin", "Release", "llama-server.exe")
	}
	if got != want {
		t.Fatalf("platformLlamaServerPath() = %q, want %q", got, want)
	}
}

func TestResolveAllowlistPathCreatesFirstRunFile(t *testing.T) {
	workingDirectory, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	temporaryWorkingDirectory := t.TempDir()
	if err := os.Chdir(temporaryWorkingDirectory); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(workingDirectory) })
	configRoot := t.TempDir()
	t.Setenv("APPDATA", configRoot)
	t.Setenv("XDG_CONFIG_HOME", configRoot)
	path, err := resolveAllowlistPath()
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "nodes: []\n" {
		t.Fatalf("first-run allowlist = %q", data)
	}
}

type fakeCommandClient struct {
	calls []string
}

type fakeModelGatewayClient struct {
	actions []string
}

func (c *fakeModelGatewayClient) States() (map[string]gatewayModelState, error) {
	return map[string]gatewayModelState{}, nil
}
func (c *fakeModelGatewayClient) Action(modelID, action string) error {
	c.actions = append(c.actions, action+" "+modelID)
	return nil
}
func (c *fakeModelGatewayClient) Refresh() error { return nil }
func (c *fakeModelGatewayClient) Plan(modelID string) (*ModelPlacementPlan, error) {
	return &ModelPlacementPlan{Model: modelID}, nil
}

func TestUnloadModelUsesInjectableGatewayClient(t *testing.T) {
	client := &fakeModelGatewayClient{}
	app := &OrchestratorApp{modelGateway: client}
	if err := app.UnloadModel("model-a"); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(client.actions, ","); got != "unload model-a" {
		t.Fatalf("gateway actions = %q, want unload model-a", got)
	}
}

func TestAggregateNodeUsage(t *testing.T) {
	sample, ok := aggregateNodeUsage(time.Date(2026, time.September, 20, 12, 0, 0, 0, time.UTC), []agent.GPUCapability{
		{VRAMBytes: 16, VRAMFreeBytes: 4, UtilizationPercent: 40},
		{VRAMBytes: 8, VRAMFreeBytes: 6, UtilizationPercent: 20},
	})
	if !ok {
		t.Fatal("aggregateNodeUsage returned no sample")
	}
	if sample.GPUPercent != 30 {
		t.Errorf("GPUPercent = %d, want 30", sample.GPUPercent)
	}
	if sample.VRAMPercent != 58 {
		t.Errorf("VRAMPercent = %d, want 58", sample.VRAMPercent)
	}
	if sample.ObservedAt != "2026-09-20T12:00:00Z" {
		t.Errorf("ObservedAt = %q", sample.ObservedAt)
	}
}

func TestValidContentRange(t *testing.T) {
	if !validContentRange("bytes 1024-12109566623/12109566624", 1024) {
		t.Fatal("validContentRange rejected a valid resumable response")
	}
	for _, value := range []string{
		"bytes 1023-12109566623/12109566624",
		"bytes 1024-12109566623/99",
		"not a range",
	} {
		if validContentRange(value, 1024) {
			t.Fatalf("validContentRange(%q) accepted an invalid range", value)
		}
	}
}

func (c *fakeCommandClient) StartRPCServer(addr string, port int) (*agent.StatusResult, error) {
	c.calls = append(c.calls, "start "+addr+" "+strconv.Itoa(port))
	return &agent.StatusResult{Status: "Running"}, nil
}

func (c *fakeCommandClient) StopRPCServer(addr string) (*agent.StatusResult, error) {
	c.calls = append(c.calls, "stop "+addr)
	return &agent.StatusResult{Status: "Stopped"}, nil
}

func (c *fakeCommandClient) GetStatus(addr string) (*agent.StatusResult, error) {
	c.calls = append(c.calls, "status "+addr)
	return &agent.StatusResult{Status: "Stopped"}, nil
}

func TestControlNodeSendsStatusStartAndStop(t *testing.T) {
	client := &fakeCommandClient{}
	node := &registry.Node{Hostname: "node-alpha", RPCPort: 50052}
	input := bufio.NewScanner(strings.NewReader("status\nstart\n\nstop\nquit\n"))
	var output bytes.Buffer

	if err := controlNode(input, &output, client, node, "100.64.0.1:7420"); err != nil {
		t.Fatalf("controlNode returned an error: %v", err)
	}

	wantCalls := []string{
		"status 100.64.0.1:7420",
		"status 100.64.0.1:7420",
		"start 100.64.0.1:7420 50052",
		"stop 100.64.0.1:7420",
	}
	if got := strings.Join(client.calls, "\n"); got != strings.Join(wantCalls, "\n") {
		t.Errorf("command calls = %q, want %q", got, strings.Join(wantCalls, "\n"))
	}
	for _, want := range []string{"Status: Stopped", "Status: Running"} {
		if !strings.Contains(output.String(), want) {
			t.Errorf("output did not contain %q:\n%s", want, output.String())
		}
	}
}

func TestStartRPCServerSkipsStartWhenAlreadyRunning(t *testing.T) {
	client := &fakeCommandClient{}
	clientStatus := &runningCommandClient{fakeCommandClient: client}
	node := &registry.Node{Hostname: "node-alpha", RPCPort: 50052}
	input := bufio.NewScanner(strings.NewReader("\n"))
	var output bytes.Buffer

	if err := startRPCServer(input, &output, clientStatus, node, "100.64.0.1:7420"); err != nil {
		t.Fatalf("startRPCServer returned an error: %v", err)
	}
	if got := strings.Join(client.calls, "\n"); got != "status 100.64.0.1:7420" {
		t.Errorf("calls = %q, want only a status check", got)
	}
	if !strings.Contains(output.String(), "already running") {
		t.Errorf("output did not explain the skipped start: %s", output.String())
	}
}

type runningCommandClient struct{ *fakeCommandClient }

func (c *runningCommandClient) GetStatus(addr string) (*agent.StatusResult, error) {
	c.calls = append(c.calls, "status "+addr)
	return &agent.StatusResult{Status: "Running"}, nil
}

func TestControlNodeRejectsInvalidPortBeforeSendingStart(t *testing.T) {
	client := &fakeCommandClient{}
	node := &registry.Node{Hostname: "node-alpha", RPCPort: 50052}
	input := bufio.NewScanner(strings.NewReader("start\nnot-a-port\nquit\n"))
	var output bytes.Buffer

	if err := controlNode(input, &output, client, node, "100.64.0.1:7420"); err != nil {
		t.Fatalf("controlNode returned an error: %v", err)
	}
	if len(client.calls) != 0 {
		t.Errorf("invalid port sent commands: %v", client.calls)
	}
	if !strings.Contains(output.String(), "RPC port must be a number between 1 and 65535") {
		t.Errorf("output did not explain invalid port:\n%s", output.String())
	}
}

func TestParsePort(t *testing.T) {
	for _, test := range []struct {
		input string
		want  int
		ok    bool
	}{
		{input: "", want: 50052, ok: true},
		{input: " 7000 ", want: 7000, ok: true},
		{input: "0"},
		{input: "65536"},
		{input: "not-a-port"},
	} {
		got, err := parsePort(test.input, 50052)
		if (err == nil) != test.ok || got != test.want {
			t.Errorf("parsePort(%q) = (%d, %v), want (%d, success=%t)", test.input, got, err, test.want, test.ok)
		}
	}
}
