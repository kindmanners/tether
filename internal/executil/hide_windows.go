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
