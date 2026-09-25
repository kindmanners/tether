// Copyright (C) 2026 kindmanners on github
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.

package trust

import (
	"crypto/tls"
	"net"
	"testing"
	"time"

	"tether/internal/certs"
)

func TestPinnedTLSConfigAcceptsMatchingPeerAndRejectsSubstitution(t *testing.T) {
	configHome := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", configHome)
	t.Setenv("APPDATA", configHome)
	agentIdentity, err := certs.LoadOrCreate("tls-agent")
	if err != nil {
		t.Fatal(err)
	}
	orchestratorIdentity, err := certs.LoadOrCreate("tls-orchestrator")
	if err != nil {
		t.Fatal(err)
	}
	if err := Pin("tls-agent", agentIdentity.CertDER); err != nil {
		t.Fatal(err)
	}
	if err := Pin("tls-orchestrator", orchestratorIdentity.CertDER); err != nil {
		t.Fatal(err)
	}
	serverConfig, err := PinnedTLSConfig(agentIdentity.TLSCertificate(), "tls-orchestrator", true)
	if err != nil {
		t.Fatal(err)
	}
	clientConfig, err := PinnedTLSConfig(orchestratorIdentity.TLSCertificate(), "tls-agent", false)
	if err != nil {
		t.Fatal(err)
	}
	serverErr, clientErr := tlsHandshake(serverConfig, clientConfig)
	if serverErr != nil || clientErr != nil {
		t.Fatalf("matching handshake: server error %v, client error %v", serverErr, clientErr)
	}

	impostor := &tls.Config{
		Certificates:       []tls.Certificate{agentIdentity.TLSCertificate()},
		InsecureSkipVerify: true, // The server-side exact pin is the assertion under test.
	}
	serverErr, _ = tlsHandshake(serverConfig, impostor)
	if serverErr == nil {
		t.Fatal("server accepted a substituted client certificate")
	}
}

func tlsHandshake(serverConfig, clientConfig *tls.Config) (error, error) {
	listener, err := tls.Listen("tcp", "127.0.0.1:0", serverConfig)
	if err != nil {
		return err, nil
	}
	defer listener.Close()
	serverResult := make(chan error, 1)
	go func() {
		connection, err := listener.Accept()
		if err == nil {
			err = connection.(*tls.Conn).Handshake()
			_ = connection.Close()
		}
		serverResult <- err
	}()
	connection, clientErr := tls.DialWithDialer(&net.Dialer{Timeout: 2 * time.Second}, "tcp", listener.Addr().String(), clientConfig)
	if connection != nil {
		_ = connection.Close()
	}
	select {
	case serverErr := <-serverResult:
		return serverErr, clientErr
	case <-time.After(3 * time.Second):
		return &net.DNSError{Err: "server handshake timed out"}, clientErr
	}
}
