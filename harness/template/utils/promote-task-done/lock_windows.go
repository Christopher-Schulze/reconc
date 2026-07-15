//go:build windows

package main

import (
	"fmt"
	"os"
	"syscall"
	"unsafe"
)

const (
	promoteLockExclusive = 0x00000002
	promoteLockFailFast  = 0x00000001
)

var (
	promoteKernel32   = syscall.NewLazyDLL("kernel32.dll")
	promoteLockFile   = promoteKernel32.NewProc("LockFileEx")
	promoteUnlockFile = promoteKernel32.NewProc("UnlockFileEx")
)

func tryPromoteLock(file *os.File) (func() error, error) {
	handle := syscall.Handle(file.Fd())
	overlapped := &syscall.Overlapped{}
	result, _, callErr := promoteLockFile.Call(
		uintptr(handle),
		uintptr(promoteLockExclusive|promoteLockFailFast),
		0,
		1,
		0,
		uintptr(unsafe.Pointer(overlapped)),
	)
	if result == 0 {
		return nil, fmt.Errorf("LockFileEx: %w", callErr)
	}
	return func() error {
		result, _, callErr := promoteUnlockFile.Call(
			uintptr(handle),
			0,
			1,
			0,
			uintptr(unsafe.Pointer(overlapped)),
		)
		if result == 0 {
			return fmt.Errorf("UnlockFileEx: %w", callErr)
		}
		return nil
	}, nil
}
