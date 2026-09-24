// Copyright (C) 2026 kindmanners on github
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.

//go:build !linux

package executil

import (
	"fmt"
	"os/exec"
)

// StartWithParentLifetime starts cmd and applies the platform lifetime guard.
func StartWithParentLifetime(cmd *exec.Cmd) (func(), error) {
	if err := cmd.Start(); err != nil {
		return func() {}, err
	}
	release, err := BindToParentLifetime(cmd)
	if err != nil {
		_ = KillProcessTree(cmd)
		_ = cmd.Wait()
		return func() {}, fmt.Errorf("binding child lifetime: %w", err)
	}
	return release, nil
}
