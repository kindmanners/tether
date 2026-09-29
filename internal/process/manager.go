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
	"net"
	"os"
	"os/exec"
	"strconv"
	"sync"
	"time"

	"tether/internal/executil"
)

// StartParams are the fully-resolved, already-validated parameters needed to
// start ggml-rpc-server. BinaryPath comes exclusively from the Agent's local
// configuration; Port is validated by the command server. The host is always
// the package-owned IPv4 loopback address.
type StartParams struct {
	BinaryPath string // from agentconfig.Config.RPCServerPath
	Port       int    // validated port number
	testMode   string
}

const rpcLoopbackHost = "127.0.0.1"

var rpcDialTimeout = 2 * time.Second

// Status describes the current state of the managed process.
type Status int

const (
	StatusStopped Status = iota
	StatusRunning
	StatusStopping
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
	case StatusStopping:
		return "Stopping"
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
	mu               sync.Mutex
	cmd              *exec.Cmd
	params           StartParams
	stopping         bool
	crashed          bool
	lastExitErr      error         // set by watchForExit when crashed becomes true; see LastExitError
	exitWatchCh      chan struct{} // closed when the exit-watching goroutine has recorded the process's outcome
	processDone      chan struct{}
	generation       uint64
	generationCtx    context.Context
	cancelGeneration context.CancelFunc
	leases           map[*RPCLease]struct{}
}

// RPCLease is a connection to the currently managed RPC process. The Manager
// closes every lease when that exact process generation stops or crashes.
type RPCLease struct {
	net.Conn
	manager    *Manager
	generation uint64
	once       sync.Once
}

func (l *RPCLease) Close() error {
	err := l.Conn.Close()
	l.once.Do(func() { l.manager.releaseLease(l) })
	return err
}

// NewManager creates an idle Manager with nothing running.
func NewManager() *Manager {
	return &Manager{leases: make(map[*RPCLease]struct{})}
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
		"--host", rpcLoopbackHost,
		"--port", fmt.Sprintf("%d", params.Port),
	)
	if params.testMode != "" {
		cmd.Env = append(os.Environ(), "TETHER_PROCESS_TEST_MODE="+params.testMode)
	}
	// Keep the RPC server in an isolated process tree and bind its lifetime to
	// the Agent. On Linux this applies a parent-death signal from a dedicated
	// OS thread; on Windows it assigns the process to a kill-on-close Job
	// Object. An abrupt Agent exit therefore cannot leave an unmanaged RPC
	// endpoint behind.
	executil.IsolateProcessTree(cmd)
	// ggml-rpc-server reports device discovery, bind failures, and backend
	// initialization details on its standard streams. Forward them through the
	// Agent so an operator can diagnose a real hardware launch; leaving these
	// nil would discard them on the platform null device.
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	releaseLifetime, err := executil.StartWithParentLifetime(cmd)
	if err != nil {
		return fmt.Errorf("starting rpc-server: %w", err)
	}

	m.cmd = cmd
	m.params = params
	m.stopping = false
	m.crashed = false
	m.lastExitErr = nil
	m.exitWatchCh = make(chan struct{})
	m.processDone = make(chan struct{})
	m.generation++
	m.generationCtx, m.cancelGeneration = context.WithCancel(context.Background())
	generation := m.generation
	processDone := m.processDone
	exitWatchCh := m.exitWatchCh

	// Wait() must be called eventually or the process becomes a zombie
	// once it exits (on POSIX) — but calling it here directly would
	// block Start() until the subprocess exits, which defeats the point
	// of Start being an async "launch and return" call. Running Wait in
	// its own goroutine lets Start return immediately while still
	// reaping the process and noticing an unexpected exit.
	go m.watchForExit(cmd, generation, processDone, exitWatchCh, releaseLifetime)

	return nil
}

// watchForExit blocks until cmd exits, then records whether that exit
// was expected (via Stop, which sets stopping before terminating the
// process) or unexpected (a real crash). Runs in its own goroutine,
// started by Start.
func (m *Manager) watchForExit(cmd *exec.Cmd, generation uint64, processDone, exitWatchCh chan struct{}, releaseLifetime func()) {
	err := cmd.Wait() // blocks until the process exits, however it exits
	releaseLifetime()
	close(processDone)

	m.mu.Lock()
	// Stop marks the process as stopping before terminating it. That state is
	// deliberately separate from a crash so the kill result used for an
	// intentional shutdown is never exposed as LastExitError.
	if m.cmd == cmd && m.generation == generation {
		leases := m.invalidateGenerationLocked()
		if m.stopping {
			m.cmd = nil
			m.stopping = false
			m.crashed = false
			m.lastExitErr = nil
		} else {
			m.crashed = true
		}
		if m.crashed && err != nil {
			// err is expected and non-nil for almost any exit that wasn't
			// a clean status-0 return; logged at the call site via
			// Status()/LastError() rather than here, since this package
			// has no logging story of its own yet — surfacing state, not
			// printing, is this package's job.
			m.lastExitErr = err
		}
		m.mu.Unlock()
		closeLeases(leases)
	} else {
		m.mu.Unlock()
	}
	close(exitWatchCh)
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
	m.stopping = true
	leases := m.invalidateGenerationLocked()
	m.mu.Unlock()
	closeLeases(leases)

	killErr := executil.KillProcessTree(cmd)

	// Wait for watchForExit's own cmd.Wait() to actually complete, rather
	// than returning as soon as Kill() is sent — Kill() only requests
	// termination, it doesn't confirm the process has actually exited.
	select {
	case <-waitCh:
	case <-ctx.Done():
		// Prefer a completed exit if it raced with context cancellation.
		select {
		case <-waitCh:
			break
		default:
			if killErr != nil {
				return fmt.Errorf("stopping rpc-server (pid %d) failed (%v) and the process did not exit before context deadline: %w", cmd.Process.Pid, killErr, ctx.Err())
			}
			return fmt.Errorf("stop requested but process did not exit before context deadline: %w", ctx.Err())
		}
	}

	return nil
}

// AcquireRPC establishes a connection to the loopback-only RPC server owned
// by the current process generation. The destination never comes from the
// network caller. A generation change during establishment fails closed.
func (m *Manager) AcquireRPC(ctx context.Context) (*RPCLease, error) {
	m.mu.Lock()
	if m.cmd == nil || m.stopping || m.crashed || m.generationCtx == nil {
		m.mu.Unlock()
		return nil, fmt.Errorf("rpc-server is not running")
	}
	cmd := m.cmd
	port := m.params.Port
	generation := m.generation
	generationCtx := m.generationCtx
	processDone := m.processDone
	m.mu.Unlock()

	dialCtx, cancel := context.WithTimeout(ctx, rpcDialTimeout)
	stopCancel := context.AfterFunc(generationCtx, cancel)
	conn, err := (&net.Dialer{}).DialContext(dialCtx, "tcp", net.JoinHostPort(rpcLoopbackHost, strconv.Itoa(port)))
	stopCancel()
	cancel()
	if err != nil {
		return nil, fmt.Errorf("connecting to managed rpc-server: %w", err)
	}

	select {
	case <-processDone:
		conn.Close()
		return nil, fmt.Errorf("rpc-server process exited during connection establishment")
	default:
	}

	lease := &RPCLease{Conn: conn, manager: m, generation: generation}
	m.mu.Lock()
	valid := m.cmd == cmd && m.generation == generation && !m.stopping && !m.crashed && m.generationCtx == generationCtx
	if valid {
		select {
		case <-generationCtx.Done():
			valid = false
		default:
		}
	}
	if valid {
		m.leases[lease] = struct{}{}
	}
	m.mu.Unlock()
	if !valid {
		conn.Close()
		return nil, fmt.Errorf("rpc-server generation changed during connection establishment")
	}
	return lease, nil
}

func (m *Manager) releaseLease(lease *RPCLease) {
	m.mu.Lock()
	delete(m.leases, lease)
	m.mu.Unlock()
}

func (m *Manager) invalidateGenerationLocked() []*RPCLease {
	if m.cancelGeneration != nil {
		m.cancelGeneration()
		m.cancelGeneration = nil
		m.generationCtx = nil
	}
	leases := make([]*RPCLease, 0, len(m.leases))
	for lease := range m.leases {
		leases = append(leases, lease)
		delete(m.leases, lease)
	}
	return leases
}

func closeLeases(leases []*RPCLease) {
	for _, lease := range leases {
		_ = lease.Close()
	}
}

// Status reports whether a process is currently running, was explicitly
// stopped, or crashed unexpectedly since last checked.
func (m *Manager) Status() Status {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.cmd == nil {
		return StatusStopped
	}
	if m.stopping {
		return StatusStopping
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
