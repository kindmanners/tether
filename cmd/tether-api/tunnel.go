// Copyright (C) 2026 kindmanners on github
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.

package main

import (
	"context"
	"fmt"
	"io"
	"net"
	"sync"

	"tether/internal/agent"
	"tether/internal/certs"
	"tether/internal/placement"
	"tether/internal/trust"
)

type tunnelPair struct {
	local  net.Conn
	remote net.Conn
}

type localAgentTunnel struct {
	listener     net.Listener
	dialRPC      func(context.Context, string) (net.Conn, error)
	agentAddress string
	ctx          context.Context
	cancel       context.CancelFunc
	mu           sync.Mutex
	closing      bool
	pairs        map[*tunnelPair]struct{}
	wg           sync.WaitGroup
	once         sync.Once
}

func openAgentTunnel(parent context.Context, node placement.Node) (workerTunnel, error) {
	if node.Local || node.Hostname == "" || node.AgentAddress == "" {
		return nil, fmt.Errorf("remote Agent tunnel requires a hostname and Agent control address")
	}
	identity, err := certs.LoadOrCreate(orchestratorIdentityName)
	if err != nil {
		return nil, fmt.Errorf("loading Orchestrator identity: %w", err)
	}
	tlsConfig, err := trust.PinnedTLSConfig(identity.TLSCertificate(), node.Hostname, false)
	if err != nil {
		return nil, fmt.Errorf("loading exact certificate pin for Agent %q: %w", node.Hostname, err)
	}
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("opening loopback tunnel for Agent %q: %w", node.Hostname, err)
	}
	ctx, cancel := context.WithCancel(parent)
	tunnel := &localAgentTunnel{
		listener: listener, dialRPC: agent.NewClient(tlsConfig).DialRPC, agentAddress: node.AgentAddress,
		ctx: ctx, cancel: cancel, pairs: make(map[*tunnelPair]struct{}),
	}
	tunnel.wg.Add(1)
	go tunnel.accept()
	return tunnel, nil
}

func (t *localAgentTunnel) Endpoint() string { return t.listener.Addr().String() }

func (t *localAgentTunnel) accept() {
	defer t.wg.Done()
	for {
		local, err := t.listener.Accept()
		if err != nil {
			return
		}
		t.wg.Add(1)
		go t.forward(local)
	}
}

func (t *localAgentTunnel) forward(local net.Conn) {
	defer t.wg.Done()
	remote, err := t.dialRPC(t.ctx, t.agentAddress)
	if err != nil {
		_ = local.Close()
		return
	}
	pair := &tunnelPair{local: local, remote: remote}
	t.mu.Lock()
	if t.closing {
		t.mu.Unlock()
		_ = local.Close()
		_ = remote.Close()
		return
	}
	t.pairs[pair] = struct{}{}
	t.mu.Unlock()

	done := make(chan struct{}, 2)
	go func() { _, _ = io.Copy(remote, local); closeWriteConn(remote); done <- struct{}{} }()
	go func() { _, _ = io.Copy(local, remote); closeWriteConn(local); done <- struct{}{} }()
	<-done
	_ = local.Close()
	_ = remote.Close()
	<-done
	t.mu.Lock()
	delete(t.pairs, pair)
	t.mu.Unlock()
}

func closeWriteConn(conn net.Conn) {
	if closer, ok := conn.(interface{ CloseWrite() error }); ok {
		_ = closer.CloseWrite()
	}
}

func (t *localAgentTunnel) Close() error {
	t.once.Do(func() {
		t.mu.Lock()
		t.closing = true
		t.cancel()
		_ = t.listener.Close()
		pairs := make([]*tunnelPair, 0, len(t.pairs))
		for pair := range t.pairs {
			pairs = append(pairs, pair)
		}
		t.mu.Unlock()
		for _, pair := range pairs {
			_ = pair.local.Close()
			_ = pair.remote.Close()
		}
		t.wg.Wait()
	})
	return nil
}
