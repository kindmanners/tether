// Copyright (C) 2026 kindmanners on github
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.

//go:build windows

package executil

import (
	"fmt"
	"os/exec"
	"unsafe"

	"golang.org/x/sys/windows"
)

// BindToParentLifetime assigns a started process to a kill-on-close Job
// Object. The OS closes the gateway's handle on crashes, terminating the
// worker and every descendant it creates.
func BindToParentLifetime(cmd *exec.Cmd) (func(), error) {
	if cmd == nil || cmd.Process == nil {
		return func() {}, fmt.Errorf("process has not started")
	}
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return func() {}, err
	}
	cleanup := func() { _ = windows.CloseHandle(job) }
	var info windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION
	info.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, err = windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info))); err != nil {
		cleanup()
		return func() {}, err
	}
	process, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(cmd.Process.Pid))
	if err != nil {
		cleanup()
		return func() {}, err
	}
	defer windows.CloseHandle(process)
	if err := windows.AssignProcessToJobObject(job, process); err != nil {
		cleanup()
		return func() {}, err
	}
	return cleanup, nil
}
