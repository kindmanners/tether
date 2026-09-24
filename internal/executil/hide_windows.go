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

//go:build windows

// Package executil contains small, platform-specific process launch helpers.
package executil

import (
	"os/exec"
	"syscall"
)

// HideWindow prevents background console programs from flashing a Command
// Prompt window when launched by a Wails desktop application.
func HideWindow(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: 0x08000000, // CREATE_NO_WINDOW
	}
}

// IsolateProcessTree gives a background command its own Windows process group.
// KillProcessTree can then terminate the command and any model workers it
// started if a graceful shutdown fails.
func IsolateProcessTree(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: 0x08000200, // CREATE_NO_WINDOW | CREATE_NEW_PROCESS_GROUP
	}
}
