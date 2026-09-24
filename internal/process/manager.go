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

// Package process manages the lifecycle of the llama.cpp rpc-server
// subprocess on an Agent machine — starting it, stopping it cleanly, and
// tracking whether it's currently running. This package trusts its
// inputs completely (a validated port number, a resolved model path from
// agentconfig.Resolve) — it does NOT re-validate them, since enforcing
// "only approved values reach here" is agentconfig's and the command
// server's job, not this package's. Keeping that separation means this
// package's own logic (process spawning, lifecycle tracking) isn't
// tangled up with unrelated input-validation concerns.
package process

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"sync"
	"time"

	"tether/internal/executil"
)

// StartParams are the fully-resolved, already-validated parameters needed to
// start ggml-rpc-server. BinaryPath and Host come exclusively from the
// Agent's local configuration; Port is validated by the command server.
type StartParams struct {
	BinaryPath string // from agentconfig.Config.RPCServerPath
	Host       string // from agentconfig.Config.RPCListenHost
	Port       int    // validated port number
}

// Status describes the current state of the managed process.
type Status int

const (
	StatusStopped Status = iota
	StatusRunning
	// StatusCrashed means the process was running but exited on its own
	// (not via Stop) since the last time its state was checked — distinct
	// from StatusStopped so a caller can tell "never started" / "we
	// stopped it" apart from "it died unexpectedly," which likely wants
	// different handling (e.g. surfacing an alert) even though both are
	// "not currently running."
	StatusCrashed
)

func (s Status) String() string {
	switch s {
	case StatusRunning:
		return "Running"
	case StatusCrashed:
		return "Crashed"
	default:
		return "Stopped"
	}
}

// Manager tracks and controls exactly one rpc-server process at a time
// (a design decision made explicitly, not a limitation to work around
// later without reconsidering it — see design doc discussion). A second
// Start call while one is already running fails rather than starting a
// second instance.
type Manager struct {
	mu          sync.Mutex
	cmd         *exec.Cmd
	params      StartParams
	crashed     bool
	lastExitErr error         // set by watchForExit when crashed becomes true; see LastExitError
	exitWatchCh chan struct{} // closed when the exit-watching goroutine has recorded the process's outcome
}

// NewManager creates an idle Manager with nothing running.
func NewManager() *Manager {
	return &Manager{}
}

// Start launches rpc-server with the given, already-validated params.
// Fails if a process is already running — callers must Stop it first.
func (m *Manager) Start(params StartParams) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.cmd != nil && !m.crashed {
		return fmt.Errorf("rpc-server is already running (pid %d) — stop it first", m.cmd.Process.Pid)
	}

	// ggml-rpc-server exposes local accelerator devices. It does NOT load a
	// model: the Orchestrator-side llama-cli or llama-server loads the GGUF
	// model and connects to this endpoint with --rpc. Host is local Agent
	// configuration, never a remote command parameter.
	cmd := exec.Command(params.BinaryPath,
		"--host", params.Host,
		"--port", fmt.Sprintf("%d", params.Port),
	)
	executil.HideWindow(cmd)
	// ggml-rpc-server reports device discovery, bind failures, and backend
	// initialization details on its standard streams. Forward them through the
	// Agent so an operator can diagnose a real hardware launch; leaving these
	// nil would discard them on the platform null device.
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("starting rpc-server: %w", err)
	}

	m.cmd = cmd
	m.params = params
	m.crashed = false
	m.lastExitErr = nil
	m.exitWatchCh = make(chan struct{})

	// Wait() must be called eventually or the process becomes a zombie
	// once it exits (on POSIX) — but calling it here directly would
	// block Start() until the subprocess exits, which defeats the point
	// of Start being an async "launch and return" call. Running Wait in
	// its own goroutine lets Start return immediately while still
	// reaping the process and noticing an unexpected exit.
	go m.watchForExit(cmd)

	return nil
}

// watchForExit blocks until cmd exits, then records whether that exit
// was expected (via Stop, which is signaled by cmd being cleared under
// the lock before this goroutine's Wait returns) or unexpected (a real
// crash). Runs in its own goroutine, started by Start.
func (m *Manager) watchForExit(cmd *exec.Cmd) {
	err := cmd.Wait() // blocks until the process exits, however it exits

	m.mu.Lock()
	defer m.mu.Unlock()

	// If m.cmd no longer points at this exact cmd, Stop already cleared
	// it deliberately — this exit was expected, nothing to flag.
	if m.cmd == cmd {
		m.crashed = true
		if err != nil {
			// err is expected and non-nil for almost any exit that wasn't
			// a clean status-0 return; logged at the call site via
			// Status()/LastError() rather than here, since this package
			// has no logging story of its own yet — surfacing state, not
			// printing, is this package's job.
			m.lastExitErr = err
		}
	}
	close(m.exitWatchCh)
}

// Stop terminates the running process, if any, and waits for it to
// actually exit before returning. Does nothing (returns nil) if nothing
// is running — Stop is idempotent, not an error to call when already
// stopped.
func (m *Manager) Stop(ctx context.Context) error {
	m.mu.Lock()
	if m.cmd == nil || m.crashed {
		m.mu.Unlock()
		return nil
	}
	cmd := m.cmd
	waitCh := m.exitWatchCh
	m.mu.Unlock()

	if err := cmd.Process.Kill(); err != nil {
		return fmt.Errorf("stopping rpc-server (pid %d): %w", cmd.Process.Pid, err)
	}

	// Wait for watchForExit's own cmd.Wait() to actually complete, rather
	// than returning as soon as Kill() is sent — Kill() only requests
	// termination, it doesn't confirm the process has actually exited.
	select {
	case <-waitCh:
	case <-ctx.Done():
		return fmt.Errorf("stop requested but process did not exit before context deadline: %w", ctx.Err())
	}

	m.mu.Lock()
	m.cmd = nil
	m.crashed = false
	m.mu.Unlock()

	return nil
}

// Status reports whether a process is currently running, was explicitly
// stopped, or crashed unexpectedly since last checked.
func (m *Manager) Status() Status {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.cmd == nil {
		return StatusStopped
	}
	if m.crashed {
		return StatusCrashed
	}
	return StatusRunning
}

// LastExitError returns the error captured from the most recent
// unexpected exit (StatusCrashed), or nil if the process hasn't crashed —
// either because it's still running, was never started, or was stopped
// cleanly via Stop (which clears this along with the rest of the crashed
// state once acknowledged).
func (m *Manager) LastExitError() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.lastExitErr
}

// waitTimeout is how long Stop waits for graceful process exit
// confirmation by default when callers don't supply their own context —
// see StopDefault.
const waitTimeout = 10 * time.Second

// StopDefault is a convenience wrapper around Stop using waitTimeout,
// for callers (like the eventual command server) that don't need custom
// deadline control.
func (m *Manager) StopDefault() error {
	ctx, cancel := context.WithTimeout(context.Background(), waitTimeout)
	defer cancel()
	return m.Stop(ctx)
}
