//go:build windows

package filelock

import (
	"fmt"
	"os"
	"syscall"
	"unsafe"
)

const (
	lockfileFailImmediately = 0x00000001
	lockfileExclusiveLock   = 0x00000002
)

var (
	kernel32         = syscall.NewLazyDLL("kernel32.dll")
	procLockFileEx   = kernel32.NewProc("LockFileEx")
	procUnlockFileEx = kernel32.NewProc("UnlockFileEx")
)

// Lock takes a blocking exclusive lock on file and returns its unlock
// function. The caller must keep file open until after unlock.
func Lock(file *os.File) (func() error, error) {
	return lock(file, lockfileExclusiveLock)
}

// TryLock takes an exclusive lock without waiting for another owner.
func TryLock(file *os.File) (func() error, error) {
	return lock(file, lockfileExclusiveLock|lockfileFailImmediately)
}

func lock(file *os.File, flags uint32) (func() error, error) {
	handle := syscall.Handle(file.Fd())
	overlapped := &syscall.Overlapped{}
	if err := lockFileEx(handle, flags, 0, 1, 0, overlapped); err != nil {
		return nil, err
	}
	return func() error {
		return unlockFileEx(handle, 0, 1, 0, overlapped)
	}, nil
}

func lockFileEx(handle syscall.Handle, flags, reserved, lowBytes, highBytes uint32, overlapped *syscall.Overlapped) error {
	r1, _, err := procLockFileEx.Call(
		uintptr(handle),
		uintptr(flags),
		uintptr(reserved),
		uintptr(lowBytes),
		uintptr(highBytes),
		uintptr(unsafe.Pointer(overlapped)),
	)
	if r1 == 0 {
		return fmt.Errorf("LockFileEx: %w", err)
	}
	return nil
}

func unlockFileEx(handle syscall.Handle, reserved, lowBytes, highBytes uint32, overlapped *syscall.Overlapped) error {
	r1, _, err := procUnlockFileEx.Call(
		uintptr(handle),
		uintptr(reserved),
		uintptr(lowBytes),
		uintptr(highBytes),
		uintptr(unsafe.Pointer(overlapped)),
	)
	if r1 == 0 {
		return fmt.Errorf("UnlockFileEx: %w", err)
	}
	return nil
}
