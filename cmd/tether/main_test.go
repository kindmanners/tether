package main

import (
	"bufio"
	"bytes"
	"strconv"
	"strings"
	"testing"
	"time"

	"tether/internal/agent"
	"tether/internal/registry"
)

type fakeCommandClient struct {
	calls []string
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
