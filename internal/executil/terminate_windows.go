//go:build windows

package executil

import (
	"fmt"
	"os/exec"
)

// KillProcessTree force-stops a process and its descendants after graceful
// shutdown has timed out. taskkill /T uses the process tree, not just the
// gateway PID, so llama-server workers cannot remain orphaned.
func KillProcessTree(cmd *exec.Cmd) error {
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	return exec.Command("taskkill", "/PID", fmt.Sprint(cmd.Process.Pid), "/T", "/F").Run()
}
