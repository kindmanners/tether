//go:build !windows

package executil

import "os/exec"

// HideWindow is a no-op where child processes do not create a Windows console.
func HideWindow(_ *exec.Cmd) {}
