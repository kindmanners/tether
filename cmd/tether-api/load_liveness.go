// Copyright (C) 2026 kindmanners on github
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as
// published by the Free Software Foundation, either version 3 of the
// License, or (at your option) any later version.

package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"sort"
	"strings"
	"sync"
	"time"

	"tether/internal/agent"
	"tether/internal/certs"
	"tether/internal/placement"
	"tether/internal/trust"
)

type loadLivenessAction int

const (
	loadLivenessPulse loadLivenessAction = iota
	loadLivenessEnd
)

type loadLivenessProbe func(context.Context, placement.Node, string, string, loadLivenessAction) error

type loadLivenessFailure struct {
	hostnames []string
	errors    map[string]error
}

func (e *loadLivenessFailure) Error() string {
	quoted := make([]string, len(e.hostnames))
	for index, hostname := range e.hostnames {
		quoted[index] = fmt.Sprintf("%q", hostname)
	}
	label := "Agents"
	if len(quoted) == 1 {
		label = "Agent"
	}
	return fmt.Sprintf("%s %s did not respond after the 30-second model-load confirmation; model loading stopped", label, strings.Join(quoted, ", "))
}

func isLoadLivenessFailure(err error) bool {
	var failure *loadLivenessFailure
	return errors.As(err, &failure)
}

type loadMonitor struct {
	model        string
	sessionID    string
	nodes        []placement.Node
	probe        loadLivenessProbe
	interval     time.Duration
	retryDelay   time.Duration
	probeTimeout time.Duration
	onSuspect    func([]string)
	onHealthy    func()
	ctx          context.Context
	cancel       context.CancelCauseFunc
	done         chan struct{}
	stopOnce     sync.Once
	mu           sync.Mutex
	finished     bool
	failure      error
}

func (g *gateway) startLoadMonitor(parent context.Context, model string, plan placement.Plan) (*loadMonitor, context.Context, error) {
	nodes := make([]placement.Node, 0, len(plan.Nodes))
	seen := make(map[string]bool)
	for _, node := range plan.Nodes {
		if node.Local || node.Hostname == "" || node.AgentAddress == "" || seen[node.Hostname] {
			continue
		}
		seen[node.Hostname] = true
		nodes = append(nodes, node)
	}
	if len(nodes) == 0 {
		return nil, parent, nil
	}
	sessionID, err := randomToken(32)
	if err != nil {
		return nil, nil, fmt.Errorf("creating model-load liveness session: %w", err)
	}
	probe := g.cfg.loadLivenessProbe
	if probe == nil {
		probe, err = newAgentLoadLivenessProbe(nodes)
		if err != nil {
			return nil, nil, err
		}
	}
	interval := g.cfg.loadHeartbeatInterval
	if interval <= 0 {
		interval = agent.LoadHeartbeatInterval
	}
	retryDelay := g.cfg.loadHeartbeatRetryDelay
	if retryDelay <= 0 {
		retryDelay = agent.LoadHeartbeatRetryDelay
	}
	probeTimeout := g.cfg.loadHeartbeatProbeTimeout
	if probeTimeout <= 0 {
		probeTimeout = agent.LoadHeartbeatProbeTimeout
	}
	loadCtx, cancel := context.WithCancelCause(parent)
	monitor := &loadMonitor{
		model: model, sessionID: sessionID, nodes: nodes, probe: probe,
		interval: interval, retryDelay: retryDelay, probeTimeout: probeTimeout,
		ctx: loadCtx, cancel: cancel, done: make(chan struct{}),
		onSuspect: func(hostnames []string) {
			detail := fmt.Sprintf("Agent liveness missed for %s; waiting 30 seconds before confirmation.", strings.Join(hostnames, ", "))
			g.setState(model, "loading", planNodeNames(plan), detail)
		},
		onHealthy: func() { g.setState(model, "loading", planNodeNames(plan), "") },
	}
	if err := monitor.probeWithConfirmation(loadCtx, nodes, loadLivenessPulse); err != nil {
		cancel(err)
		close(monitor.done)
		monitor.abortAgents()
		return nil, nil, err
	}
	go monitor.run()
	return monitor, loadCtx, nil
}

func (m *loadMonitor) run() {
	defer close(m.done)
	ticker := time.NewTicker(m.interval)
	defer ticker.Stop()
	for {
		select {
		case <-m.ctx.Done():
			return
		case <-ticker.C:
			if err := m.probeWithConfirmation(m.ctx, m.nodes, loadLivenessPulse); err != nil {
				if isLoadLivenessFailure(err) {
					log.Printf("model-load liveness failed for %s: %v", m.model, err)
					m.mu.Lock()
					m.failure = err
					m.mu.Unlock()
					m.cancel(err)
				}
				return
			}
		}
	}
}

func (m *loadMonitor) probeWithConfirmation(ctx context.Context, nodes []placement.Node, action loadLivenessAction) error {
	failed := m.probeRound(ctx, nodes, action)
	if len(failed) == 0 {
		if m.onHealthy != nil {
			m.onHealthy()
		}
		return nil
	}
	hostnames := sortedFailureNames(failed)
	if m.onSuspect != nil {
		m.onSuspect(hostnames)
	}
	log.Printf("model-load liveness missed model=%q agents=%q; confirming after %s", m.model, strings.Join(hostnames, ","), m.retryDelay)
	timer := time.NewTimer(m.retryDelay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
	}
	retryNodes := make([]placement.Node, 0, len(hostnames))
	for _, node := range nodes {
		if _, ok := failed[node.Hostname]; ok {
			retryNodes = append(retryNodes, node)
		}
	}
	confirmed := m.probeRound(ctx, retryNodes, action)
	if len(confirmed) == 0 {
		if m.onHealthy != nil {
			m.onHealthy()
		}
		return nil
	}
	return &loadLivenessFailure{hostnames: sortedFailureNames(confirmed), errors: confirmed}
}

func (m *loadMonitor) probeRound(ctx context.Context, nodes []placement.Node, action loadLivenessAction) map[string]error {
	type result struct {
		hostname string
		err      error
	}
	results := make(chan result, len(nodes))
	for _, node := range nodes {
		node := node
		go func() {
			probeCtx, cancel := context.WithTimeout(ctx, m.probeTimeout)
			defer cancel()
			results <- result{hostname: node.Hostname, err: m.probe(probeCtx, node, m.sessionID, m.model, action)}
		}()
	}
	failed := make(map[string]error)
	for range nodes {
		result := <-results
		if result.err != nil {
			failed[result.hostname] = result.err
		}
	}
	return failed
}

func (m *loadMonitor) Complete(ctx context.Context) error {
	m.stopLoop()
	m.mu.Lock()
	failure := m.failure
	m.mu.Unlock()
	if failure != nil {
		return failure
	}
	if err := m.probeWithConfirmation(ctx, m.nodes, loadLivenessEnd); err != nil {
		return err
	}
	m.mu.Lock()
	m.finished = true
	m.mu.Unlock()
	return nil
}

// Confirm stops the periodic loop and performs one final pulse cycle. Startup
// error paths use it to distinguish an Agent disappearing from an unrelated
// llama-server failure, so the failed hostname is not lost when the worker
// exits before the next 20-second tick.
func (m *loadMonitor) Confirm(ctx context.Context) error {
	m.stopLoop()
	m.mu.Lock()
	failure := m.failure
	m.mu.Unlock()
	if failure != nil {
		return failure
	}
	return m.probeWithConfirmation(ctx, m.nodes, loadLivenessPulse)
}

func (m *loadMonitor) Abort() {
	if m == nil {
		return
	}
	m.stopLoop()
	m.mu.Lock()
	finished := m.finished
	m.mu.Unlock()
	if !finished {
		m.abortAgents()
	}
}

func (m *loadMonitor) stopLoop() {
	m.stopOnce.Do(func() {
		m.cancel(context.Canceled)
		<-m.done
	})
}

func (m *loadMonitor) abortAgents() {
	ctx, cancel := context.WithTimeout(context.Background(), m.probeTimeout)
	defer cancel()
	_ = m.probeRound(ctx, m.nodes, loadLivenessEnd)
}

func sortedFailureNames(failures map[string]error) []string {
	names := make([]string, 0, len(failures))
	for hostname := range failures {
		names = append(names, hostname)
	}
	sort.Strings(names)
	return names
}

func newAgentLoadLivenessProbe(nodes []placement.Node) (loadLivenessProbe, error) {
	identity, err := certs.LoadOrCreate(orchestratorIdentityName)
	if err != nil {
		return nil, fmt.Errorf("loading Orchestrator identity: %w", err)
	}
	clients := make(map[string]*agent.Client, len(nodes))
	for _, node := range nodes {
		tlsConfig, err := trust.PinnedTLSConfig(identity.TLSCertificate(), node.Hostname, false)
		if err != nil {
			return nil, fmt.Errorf("loading exact certificate pin for Agent %q: %w", node.Hostname, err)
		}
		clients[node.Hostname] = agent.NewClient(tlsConfig)
	}
	return func(ctx context.Context, node placement.Node, sessionID, model string, action loadLivenessAction) error {
		client := clients[node.Hostname]
		if client == nil {
			return fmt.Errorf("no liveness client for Agent %q", node.Hostname)
		}
		switch action {
		case loadLivenessPulse:
			return client.PulseLoadLiveness(ctx, node.AgentAddress, sessionID, model)
		case loadLivenessEnd:
			return client.EndLoadLiveness(ctx, node.AgentAddress, sessionID)
		default:
			return fmt.Errorf("unsupported model-load liveness action")
		}
	}, nil
}
