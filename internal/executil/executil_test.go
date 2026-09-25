// Copyright (C) 2026 kindmanners on github
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.

package executil

import "testing"

func TestKillProcessTreeIgnoresUnstartedProcess(t *testing.T) {
	if err := KillProcessTree(nil); err != nil {
		t.Fatalf("KillProcessTree(nil) = %v", err)
	}
}
