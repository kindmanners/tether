// Copyright (C) 2026 kindmanners on github
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.

//go:build windows

package executil

import (
	"os/exec"
	"testing"
)

func TestWindowsProcessAttributesAndUnstartedLifetimeGuard(t *testing.T) {
	command := exec.Command("cmd.exe", "/c", "exit", "0")
	HideWindow(command)
	if command.SysProcAttr == nil || !command.SysProcAttr.HideWindow || command.SysProcAttr.CreationFlags != 0x08000000 {
		t.Fatalf("HideWindow attributes = %#v", command.SysProcAttr)
	}
	IsolateProcessTree(command)
	if command.SysProcAttr == nil || command.SysProcAttr.CreationFlags != 0x08000200 {
		t.Fatalf("IsolateProcessTree attributes = %#v", command.SysProcAttr)
	}
	if _, err := BindToParentLifetime(exec.Command("cmd.exe")); err == nil {
		t.Fatal("BindToParentLifetime accepted an unstarted command")
	}
}
