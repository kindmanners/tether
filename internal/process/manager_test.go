// Copyright (C) 2026 kindmanners on github
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.

package process

import (
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"testing"
	"time"
)

const (
	testSleepHost = "__process_manager_test_sleep__"
	testCrashHost = "__process_manager_test_crash__"
)

// TestMain lets the package's test executable act as a controllable child for
// Manager without requiring a shell or platform-specific helper program.
func TestMain(m *testing.M) {
	helperArguments(os.Args)
	switch os.Getenv("TETHER_PROCESS_TEST_MODE") {
	case testSleepHost:
		time.Sleep(30 * time.Second)
		os.Exit(0)
	case testCrashHost:
		os.Exit(17)
	}
	os.Exit(m.Run())
}

func helperArguments(arguments []string) (string, string) {
	var host, port string
	for i := 1; i+1 < len(arguments); i++ {
		switch arguments[i] {
		case "--host":
			host = arguments[i+1]
		case "--port":
			port = arguments[i+1]
		}
	}
	return host, port
}

func TestIntentionalStopDoesNotRecordCrashError(t *testing.T) {
	manager := NewManager()
	if err := manager.Start(StartParams{BinaryPath: testExecutable(t), Port: 30, testMode: testSleepHost}); err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := manager.Stop(ctx); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
	if got := manager.Status(); got != StatusStopped {
		t.Fatalf("Status() = %s, want Stopped", got)
	}
	if err := manager.LastExitError(); err != nil {
		t.Fatalf("LastExitError() = %v after intentional stop, want nil", err)
	}
}

func TestFreshManagerIsStoppedAndIdleStopIsNoOp(t *testing.T) {
	manager := NewManager()
	if got := manager.Status(); got != StatusStopped {
		t.Fatalf("Status() = %s, want Stopped", got)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := manager.Stop(ctx); err != nil {
		t.Fatalf("Stop() on idle manager = %v", err)
	}
}

func TestDoubleStartRejectedAndRestartAfterCrashAllowed(t *testing.T) {
	manager := NewManager()
	if err := manager.Start(StartParams{BinaryPath: testExecutable(t), Port: 30, testMode: testSleepHost}); err != nil {
		t.Fatal(err)
	}
	if err := manager.Start(StartParams{BinaryPath: testExecutable(t), Port: 30, testMode: testSleepHost}); err == nil {
		t.Fatal("second Start() succeeded while process was running")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := manager.Stop(ctx); err != nil {
		t.Fatal(err)
	}

	if err := manager.Start(StartParams{BinaryPath: testExecutable(t), Port: 1, testMode: testCrashHost}); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for manager.Status() == StatusRunning && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if got := manager.Status(); got != StatusCrashed {
		t.Fatalf("Status() = %s after crash", got)
	}
	if err := manager.Start(StartParams{BinaryPath: testExecutable(t), Port: 30, testMode: testSleepHost}); err != nil {
		t.Fatalf("Start() after crash = %v", err)
	}
	if err := manager.Stop(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestUnexpectedExitRecordsCrashError(t *testing.T) {
	manager := NewManager()
	if err := manager.Start(StartParams{BinaryPath: testExecutable(t), Port: 1, testMode: testCrashHost}); err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	deadline := time.Now().Add(5 * time.Second)
	for manager.Status() == StatusRunning && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if got := manager.Status(); got != StatusCrashed {
		t.Fatalf("Status() = %s, want Crashed", got)
	}
	if err := manager.LastExitError(); err == nil {
		t.Fatal("LastExitError() = nil after unexpected exit")
	}
}

func TestAcquireRPCIsLoopbackAndLeaseClosesOnStop(t *testing.T) {
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	port := listener.Addr().(*net.TCPAddr).Port
	go func() {
		for {
			conn, acceptErr := listener.Accept()
			if acceptErr != nil {
				return
			}
			go func() {
				_, _ = io.Copy(conn, conn)
				_ = conn.Close()
			}()
		}
	}()

	manager := NewManager()
	if err := manager.Start(StartParams{BinaryPath: testExecutable(t), Port: port, testMode: testSleepHost}); err != nil {
		t.Fatal(err)
	}
	lease, err := manager.AcquireRPC(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := lease.Write([]byte("ping")); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 4)
	if _, err := io.ReadFull(lease, buf); err != nil || string(buf) != "ping" {
		t.Fatalf("echo = %q, %v", buf, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := manager.Stop(ctx); err != nil {
		t.Fatal(err)
	}
	_ = lease.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
	if _, err := lease.Read(make([]byte, 1)); err == nil {
		t.Fatal("generation lease remained open after process stop")
	}

	if err := manager.Start(StartParams{BinaryPath: testExecutable(t), Port: port, testMode: testSleepHost}); err != nil {
		t.Fatalf("restarting on the same managed port: %v", err)
	}
	newLease, err := manager.AcquireRPC(context.Background())
	if err != nil {
		t.Fatalf("acquiring restarted generation: %v", err)
	}
	defer newLease.Close()
	if _, err := newLease.Write([]byte("next")); err != nil {
		t.Fatal(err)
	}
	if _, err := io.ReadFull(newLease, buf); err != nil || string(buf) != "next" {
		t.Fatalf("restarted generation echo = %q, %v", buf, err)
	}
	if err := manager.Stop(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestAcquireRPCRejectsInactiveProcess(t *testing.T) {
	if _, err := NewManager().AcquireRPC(context.Background()); err == nil {
		t.Fatal("AcquireRPC succeeded without a managed process")
	}
}

func TestStatusStringIncludesStopping(t *testing.T) {
	if got := StatusStopping.String(); got != "Stopping" {
		t.Fatalf("StatusStopping.String() = %q, want %q", got, "Stopping")
	}
}

func testExecutable(t *testing.T) string {
	t.Helper()
	path, err := os.Executable()
	if err != nil {
		t.Fatal(fmt.Errorf("locating test executable: %w", err))
	}
	return path
}
