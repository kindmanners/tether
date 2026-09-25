// Copyright (C) 2026 kindmanners on github
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.

package pairing

import (
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"tether/internal/certs"
	"tether/internal/trust"
)

func TestClientAndServerPairEndToEndAndAllowCodeRetry(t *testing.T) {
	configHome := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", configHome)
	t.Setenv("APPDATA", configHome)
	agentIdentity, err := certs.LoadOrCreate("pairing-agent")
	if err != nil {
		t.Fatal(err)
	}
	orchestratorIdentity, err := certs.LoadOrCreate("pairing-orchestrator")
	if err != nil {
		t.Fatal(err)
	}
	pairingServer, err := NewServer(agentIdentity, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewUnstartedServer(http.HandlerFunc(pairingServer.handlePair))
	server.TLS = &tls.Config{Certificates: []tls.Certificate{agentIdentity.TLSCertificate()}}
	server.StartTLS()
	defer server.Close()

	client := NewClient(orchestratorIdentity)
	client.selfHostname = func() (string, error) { return "orchestrator-tailnet", nil }
	addr := strings.TrimPrefix(server.URL, "https://")
	if _, err := client.Pair(addr, "wrong-code"); err == nil {
		t.Fatal("Pair() accepted a wrong code")
	}
	result, err := client.Pair(addr, pairingServer.Code())
	if err != nil {
		t.Fatalf("Pair() retry with valid code failed: %v", err)
	}
	if result.AgentHostname != "pairing-agent" {
		t.Fatalf("AgentHostname = %q", result.AgentHostname)
	}
	for _, hostname := range []string{"pairing-agent", "pairing-orchestrator"} {
		if _, found, err := trust.Get(hostname); err != nil || !found {
			t.Fatalf("trust.Get(%q) = found %t, err %v", hostname, found, err)
		}
	}
	if got := pairingServer.OrchestratorTailnetHostname(); got != "orchestrator-tailnet" {
		t.Fatalf("OrchestratorTailnetHostname() = %q", got)
	}
}

func TestClientRejectsExpiredPairingWindow(t *testing.T) {
	configHome := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", configHome)
	t.Setenv("APPDATA", configHome)
	agentIdentity, err := certs.LoadOrCreate("expired-agent")
	if err != nil {
		t.Fatal(err)
	}
	orchestratorIdentity, err := certs.LoadOrCreate("expired-orchestrator")
	if err != nil {
		t.Fatal(err)
	}
	pairingServer, err := NewServer(agentIdentity, 10*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	code := pairingServer.Code()
	server := httptest.NewUnstartedServer(http.HandlerFunc(pairingServer.handlePair))
	server.TLS = &tls.Config{Certificates: []tls.Certificate{agentIdentity.TLSCertificate()}}
	server.StartTLS()
	defer server.Close()
	time.Sleep(20 * time.Millisecond)
	client := NewClient(orchestratorIdentity)
	client.selfHostname = func() (string, error) { return "orchestrator-tailnet", nil }
	if _, err := client.Pair(strings.TrimPrefix(server.URL, "https://"), code); err == nil {
		t.Fatal("Pair() accepted an expired pairing code")
	}
}
