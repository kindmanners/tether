// Copyright (C) 2026 kindmanners on github
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.

//go:build !windows

package executil

import "os/exec"

// BindToParentLifetime is handled by Pdeathsig on Linux. Other Unix systems
// retain process-group cleanup for controlled shutdown.
func BindToParentLifetime(_ *exec.Cmd) (func(), error) { return func() {}, nil }
