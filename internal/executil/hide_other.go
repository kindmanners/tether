//go:build !windows

package executil

import (
	"os/exec"
	"syscall"
)

// HideWindow is a no-op where child processes do not create a Windows console.
func HideWindow(_ *exec.Cmd) {}

// IsolateProcessTree makes the command the leader of a new process group so
// that a bounded shutdown fallback can terminate its descendants as well.
func IsolateProcessTree(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}
