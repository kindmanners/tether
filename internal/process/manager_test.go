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
	"os"
	"strconv"
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
	host, port := helperArguments(os.Args)
	switch host {
	case testSleepHost:
		seconds, _ := strconv.Atoi(port)
		time.Sleep(time.Duration(seconds) * time.Second)
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
	if err := manager.Start(StartParams{BinaryPath: testExecutable(t), Host: testSleepHost, Port: 30}); err != nil {
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

func TestUnexpectedExitRecordsCrashError(t *testing.T) {
	manager := NewManager()
	if err := manager.Start(StartParams{BinaryPath: testExecutable(t), Host: testCrashHost, Port: 1}); err != nil {
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
