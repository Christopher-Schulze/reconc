//go:build windows

package main

import (
	"fmt"
	"os"
	"syscall"
	"unsafe"
)

const auditCacheExclusiveLock = 0x00000002

var (
	auditCacheKernel32     = syscall.NewLazyDLL("kernel32.dll")
	auditCacheLockFileEx   = auditCacheKernel32.NewProc("LockFileEx")
	auditCacheUnlockFileEx = auditCacheKernel32.NewProc("UnlockFileEx")
)

func lockAuditCacheFile(file *os.File) (func() error, error) {
	handle := syscall.Handle(file.Fd())
	overlapped := &syscall.Overlapped{}
	if err := callAuditCacheLockFileEx(handle, overlapped); err != nil {
		return nil, err
	}
	return func() error {
		return callAuditCacheUnlockFileEx(handle, overlapped)
	}, nil
}

func callAuditCacheLockFileEx(handle syscall.Handle, overlapped *syscall.Overlapped) error {
	result, _, callErr := auditCacheLockFileEx.Call(
		uintptr(handle),
		uintptr(auditCacheExclusiveLock),
		0,
		1,
		0,
		uintptr(unsafe.Pointer(overlapped)),
	)
	if result == 0 {
		return fmt.Errorf("cache file lock: %w", callErr)
	}
	return nil
}

func callAuditCacheUnlockFileEx(handle syscall.Handle, overlapped *syscall.Overlapped) error {
	result, _, callErr := auditCacheUnlockFileEx.Call(
		uintptr(handle),
		0,
		1,
		0,
		uintptr(unsafe.Pointer(overlapped)),
	)
	if result == 0 {
		return fmt.Errorf("cache file unlock: %w", callErr)
	}
	return nil
}
