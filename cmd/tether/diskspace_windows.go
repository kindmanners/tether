// Copyright (C) 2026 kindmanners on github
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as
// published by the Free Software Foundation, either version 3 of the
// License, or (at your option) any later version.
//
// This program is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
// GNU Affero General Public License for more details.
//
// You should have received a copy of the GNU Affero General Public License
// along with this program.  If not, see <https://www.gnu.org/licenses/>.

//go:build windows

package main

import (
	"fmt"
	"syscall"
	"unsafe"
)

func availableDiskBytes(path string) (uint64, error) {
	kernel32 := syscall.NewLazyDLL("kernel32.dll")
	proc := kernel32.NewProc("GetDiskFreeSpaceExW")
	pathUTF16, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return 0, err
	}
	var available uint64
	result, _, callErr := proc.Call(uintptr(unsafe.Pointer(pathUTF16)), uintptr(unsafe.Pointer(&available)), 0, 0)
	if result == 0 {
		return 0, fmt.Errorf("checking free disk space: %w", callErr)
	}
	return available, nil
}
