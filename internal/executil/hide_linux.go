// Copyright (C) 2026 kindmanners on github
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.

//go:build linux

package executil

import (
	"os/exec"
	"syscall"
)

// HideWindow is a no-op where child processes do not create a Windows console.
func HideWindow(_ *exec.Cmd) {}

// IsolateProcessTree creates a process group for bounded tree termination and
// asks Linux to kill the child if the gateway process itself dies.
func IsolateProcessTree(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true, Pdeathsig: syscall.SIGKILL}
}
