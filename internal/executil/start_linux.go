// Copyright (C) 2026 kindmanners on github
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.

//go:build linux

package executil

import (
	"os/exec"
	"runtime"
	"sync"
)

// StartWithParentLifetime starts cmd from a dedicated locked OS thread and
// keeps that thread alive until release. Linux Pdeathsig is tied to the thread
// that created the child, so this prevents a healthy Go runtime from
// accidentally triggering the worker's death signal.
func StartWithParentLifetime(cmd *exec.Cmd) (func(), error) {
	release := make(chan struct{})
	started := make(chan error, 1)
	go func() {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		err := cmd.Start()
		started <- err
		if err == nil {
			<-release
		}
	}()
	if err := <-started; err != nil {
		return func() {}, err
	}
	var once sync.Once
	return func() { once.Do(func() { close(release) }) }, nil
}
