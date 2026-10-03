//go:build !windows && !darwin

// Package processgroup sends signals to explicitly owned Unix process groups.
package processgroup

import "syscall"

// Signal targets a positive owned group ID and preserves native signal errors.
func Signal(groupID int, signal syscall.Signal) error {
	if groupID <= 1 {
		return syscall.EINVAL
	}
	return syscall.Kill(-groupID, signal)
}
