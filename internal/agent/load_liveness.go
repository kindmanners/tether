// Copyright (C) 2026 kindmanners on github
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as
// published by the Free Software Foundation, either version 3 of the
// License, or (at your option) any later version.

package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"
)

const (
	// LoadHeartbeatInterval is the normal cadence while a remote model is
	// loading. The gateway confirms a missed response once after RetryDelay.
	LoadHeartbeatInterval     = 20 * time.Second
	LoadHeartbeatRetryDelay   = 30 * time.Second
	LoadHeartbeatProbeTimeout = 5 * time.Second

	// LoadHeartbeatExpiry is deliberately longer than one complete failed
	// gateway cycle: cadence + first probe + grace + confirmation probe. The
	// additional five seconds prevents the Agent watchdog racing the gateway's
	// confirmation on a slow scheduler.
	LoadHeartbeatExpiry = LoadHeartbeatInterval + 2*LoadHeartbeatProbeTimeout + LoadHeartbeatRetryDelay + 5*time.Second
)

const (
	loadLivenessActionPulse = "pulse"
	loadLivenessActionEnd   = "end"
)

type loadLivenessRequest struct {
	SessionID string `json:"sessionId"`
	Model     string `json:"model,omitempty"`
	Action    string `json:"action"`
}

type loadLivenessResponse struct {
	Reply      string `json:"reply"`
	ObservedAt string `json:"observedAt"`
}

// LoadLivenessStatus is the Agent-local view of an Orchestrator-owned model
// load. It is exposed to the Agent UI and authenticated status callers.
type LoadLivenessStatus struct {
	State    string `json:"state,omitempty"`
	Model    string `json:"model,omitempty"`
	Detail   string `json:"detail,omitempty"`
	LastSeen string `json:"lastSeen,omitempty"`
}

type processStopper interface {
	StopDefault() error
}

type loadLiveness struct {
	mu         sync.Mutex
	stopper    processStopper
	expiry     time.Duration
	timer      *time.Timer
	generation uint64
	sessionID  string
	model      string
	active     bool
	expired    bool
	status     LoadLivenessStatus
}

func newLoadLiveness(stopper processStopper, expiry time.Duration) *loadLiveness {
	return &loadLiveness{stopper: stopper, expiry: expiry}
}

func (l *loadLiveness) pulse(sessionID, model string) error {
	if err := validateLoadSession(sessionID, model); err != nil {
		return err
	}
	now := time.Now().UTC()
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.expired && l.sessionID == sessionID {
		return fmt.Errorf("model-load liveness session has expired")
	}
	if l.active && l.sessionID != sessionID {
		return fmt.Errorf("another model-load liveness session is already active")
	}
	if l.timer != nil {
		l.timer.Stop()
	}
	l.generation++
	generation := l.generation
	l.sessionID = sessionID
	l.model = model
	l.active = true
	l.expired = false
	l.status = LoadLivenessStatus{
		State: "Monitoring", Model: model,
		Detail:   "The paired Orchestrator is responding while model loading is active.",
		LastSeen: now.Format(time.RFC3339),
	}
	l.timer = time.AfterFunc(l.expiry, func() { l.expire(generation) })
	return nil
}

func (l *loadLiveness) end(sessionID string) error {
	if err := validateLoadSessionID(sessionID); err != nil {
		return err
	}
	now := time.Now().UTC()
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.expired && l.sessionID == sessionID {
		return fmt.Errorf("model-load liveness session has expired")
	}
	if l.sessionID != "" && l.sessionID != sessionID {
		return fmt.Errorf("model-load liveness session does not match the active session")
	}
	if l.timer != nil {
		l.timer.Stop()
		l.timer = nil
	}
	l.generation++
	l.active = false
	l.expired = false
	l.status = LoadLivenessStatus{
		State: "Available", Model: l.model,
		Detail:   "The paired Orchestrator responded throughout model loading.",
		LastSeen: now.Format(time.RFC3339),
	}
	return nil
}

func (l *loadLiveness) expire(generation uint64) {
	l.mu.Lock()
	if !l.active || l.generation != generation {
		l.mu.Unlock()
		return
	}
	l.active = false
	l.expired = true
	l.timer = nil
	log.Printf("agent: model-load heartbeat timeout declared session=%q model=%q at=%s expiry=%s", l.sessionID, l.model, time.Now().UTC().Format(time.RFC3339Nano), l.expiry)
	l.status = LoadLivenessStatus{
		State: "Unavailable", Model: l.model,
		Detail:   "Orchestrator Not Available: model loading stopped; terminating the managed RPC process to reclaim GPU memory.",
		LastSeen: l.status.LastSeen,
	}
	// Keep new load sessions behind the process stop. Otherwise a new session
	// can arm itself while this expired session is still terminating the old
	// RPC process, and that delayed stop can kill the new load's process.
	err := l.stopper.StopDefault()
	defer l.mu.Unlock()
	if err != nil {
		l.status.Detail = "Orchestrator Not Available: model loading stopped, but terminating the managed RPC process failed: " + err.Error()
		return
	}
	l.status.Detail = "Orchestrator Not Available: model loading stopped and the managed RPC process was terminated to reclaim GPU memory."
}

func (l *loadLiveness) Status() LoadLivenessStatus {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.status
}

func (l *loadLiveness) close() {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.timer != nil {
		l.timer.Stop()
		l.timer = nil
	}
	l.generation++
	l.active = false
}

func validateLoadSession(sessionID, model string) error {
	if err := validateLoadSessionID(sessionID); err != nil {
		return err
	}
	if strings.TrimSpace(model) == "" || len(model) > 1024 || strings.ContainsAny(model, "\x00\r\n") {
		return fmt.Errorf("model must be a non-empty single-line value")
	}
	return nil
}

func validateLoadSessionID(sessionID string) error {
	if len(sessionID) < 16 || len(sessionID) > 128 {
		return fmt.Errorf("sessionId must contain between 16 and 128 characters")
	}
	for _, character := range sessionID {
		if (character < 'a' || character > 'z') && (character < 'A' || character > 'Z') && (character < '0' || character > '9') && character != '-' && character != '_' {
			return fmt.Errorf("sessionId contains an invalid character")
		}
	}
	return nil
}

func (s *Server) handleLoadLiveness(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "only POST is supported")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxRequestBytes)
	var request loadLivenessRequest
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		writeError(w, http.StatusBadRequest, "malformed request body")
		return
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		writeError(w, http.StatusBadRequest, "request body must contain one JSON object")
		return
	}

	var err error
	switch request.Action {
	case loadLivenessActionPulse:
		err = s.loadLiveness.pulse(request.SessionID, request.Model)
	case loadLivenessActionEnd:
		err = s.loadLiveness.end(request.SessionID)
	default:
		writeError(w, http.StatusBadRequest, "action must be pulse or end")
		return
	}
	if err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(loadLivenessResponse{Reply: "pong", ObservedAt: time.Now().UTC().Format(time.RFC3339)})
}

func (c *Client) PulseLoadLiveness(ctx context.Context, addr, sessionID, model string) error {
	return c.sendLoadLiveness(ctx, addr, loadLivenessRequest{SessionID: sessionID, Model: model, Action: loadLivenessActionPulse})
}

func (c *Client) EndLoadLiveness(ctx context.Context, addr, sessionID string) error {
	return c.sendLoadLiveness(ctx, addr, loadLivenessRequest{SessionID: sessionID, Action: loadLivenessActionEnd})
}

func (c *Client) sendLoadLiveness(ctx context.Context, addr string, request loadLivenessRequest) error {
	body, err := json.Marshal(request)
	if err != nil {
		return fmt.Errorf("encoding model-load liveness request: %w", err)
	}
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://"+addr+"/load-liveness", strings.NewReader(string(body)))
	if err != nil {
		return err
	}
	httpRequest.Header.Set("Content-Type", "application/json")
	response, err := c.httpClient.Do(httpRequest)
	if err != nil {
		return fmt.Errorf("contacting agent at %s: %w", addr, err)
	}
	defer response.Body.Close()
	responseBody, err := io.ReadAll(io.LimitReader(response.Body, maxRequestBytes))
	if err != nil {
		return fmt.Errorf("reading model-load liveness response: %w", err)
	}
	if response.StatusCode != http.StatusOK {
		var failure errorResponse
		if json.Unmarshal(responseBody, &failure) == nil && failure.Error != "" {
			return fmt.Errorf("agent returned %d: %s", response.StatusCode, failure.Error)
		}
		return fmt.Errorf("agent returned unexpected status %d", response.StatusCode)
	}
	var reply loadLivenessResponse
	if err := json.Unmarshal(responseBody, &reply); err != nil || reply.Reply != "pong" {
		return fmt.Errorf("agent returned an invalid model-load liveness response")
	}
	return nil
}
