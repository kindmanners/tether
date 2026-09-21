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
