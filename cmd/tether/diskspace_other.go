//go:build !windows && !linux

package main

import "fmt"

// Windows is the supported desktop download target. Other platforms retain a
// conservative explicit error instead of silently skipping the disk-space
// release gate until a platform implementation is added.
func availableDiskBytes(string) (uint64, error) {
	return 0, fmt.Errorf("checking free disk space is not implemented on this platform")
}
