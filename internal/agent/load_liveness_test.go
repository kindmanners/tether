// Copyright (C) 2026 kindmanners on github
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as
// published by the Free Software Foundation, either version 3 of the
// License, or (at your option) any later version.

package agent

import (
	"sync/atomic"
	"testing"
	"time"
)

type recordingStopper struct {
	calls atomic.Int32
}

func (s *recordingStopper) StopDefault() error {
	s.calls.Add(1)
	return nil
}

type blockingStopper struct {
	started chan struct{}
	release chan struct{}
}

func (s *blockingStopper) StopDefault() error {
	select {
	case <-s.started:
	default:
		close(s.started)
	}
	<-s.release
	return nil
}

func TestLoadLivenessExpiryStopsRPCAndReportsUnavailable(t *testing.T) {
	stopper := &recordingStopper{}
	liveness := newLoadLiveness(stopper, 20*time.Millisecond)
	t.Cleanup(liveness.close)
	if err := liveness.pulse("session_123456789", "model.gguf"); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) && stopper.calls.Load() == 0 {
		time.Sleep(time.Millisecond)
	}
	if stopper.calls.Load() != 1 {
		t.Fatalf("StopDefault calls = %d, want 1", stopper.calls.Load())
	}
	status := liveness.Status()
	if status.State != "Unavailable" || status.Model != "model.gguf" {
		t.Fatalf("status after expiry = %#v", status)
	}
	if err := liveness.pulse("session_123456789", "model.gguf"); err == nil {
		t.Fatal("expired session was re-armed")
	}
}

func TestLoadLivenessPulseRefreshesAndEndDisarms(t *testing.T) {
	stopper := &recordingStopper{}
	liveness := newLoadLiveness(stopper, 35*time.Millisecond)
	t.Cleanup(liveness.close)
	if err := liveness.pulse("session_123456789", "model.gguf"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(20 * time.Millisecond)
	if err := liveness.pulse("session_123456789", "model.gguf"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(20 * time.Millisecond)
	if stopper.calls.Load() != 0 {
		t.Fatal("refreshed session expired too early")
	}
	if err := liveness.end("session_123456789"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(45 * time.Millisecond)
	if stopper.calls.Load() != 0 {
		t.Fatal("ended session stopped RPC")
	}
	if status := liveness.Status(); status.State != "Available" {
		t.Fatalf("ended status = %#v", status)
	}
}

func TestLoadLivenessRejectsConcurrentSession(t *testing.T) {
	liveness := newLoadLiveness(&recordingStopper{}, time.Minute)
	t.Cleanup(liveness.close)
	if err := liveness.pulse("session_123456789", "first.gguf"); err != nil {
		t.Fatal(err)
	}
	if err := liveness.pulse("different_12345678", "second.gguf"); err == nil {
		t.Fatal("concurrent session was accepted")
	}
}

func TestLoadLivenessWaitsForExpiredProcessStopBeforeNewSession(t *testing.T) {
	stopper := &blockingStopper{started: make(chan struct{}), release: make(chan struct{})}
	liveness := newLoadLiveness(stopper, 50*time.Millisecond)
	t.Cleanup(liveness.close)
	if err := liveness.pulse("expired_12345678", "first.gguf"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-stopper.started:
	case <-time.After(time.Second):
		t.Fatal("expired session did not begin stopping the RPC process")
	}

	pulseDone := make(chan error, 1)
	go func() { pulseDone <- liveness.pulse("replacement_12345", "second.gguf") }()
	select {
	case err := <-pulseDone:
		t.Fatalf("replacement session completed before the expired process stopped: %v", err)
	case <-time.After(20 * time.Millisecond):
	}
	close(stopper.release)
	select {
	case err := <-pulseDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("replacement session did not start after the expired process stopped")
	}
	if status := liveness.Status(); status.State != "Monitoring" || status.Model != "second.gguf" {
		t.Fatalf("replacement session status = %#v", status)
	}
}
