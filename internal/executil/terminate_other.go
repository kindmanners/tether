//go:build !windows

package executil

import (
	"os/exec"
	"syscall"
)

// KillProcessTree force-stops the isolated process group after graceful
// shutdown has timed out. Negative PID targets the group created by
// IsolateProcessTree, including model workers inherited from the gateway.
func KillProcessTree(cmd *exec.Cmd) error {
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
}
