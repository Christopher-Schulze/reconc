//go:build darwin

// Package processgroup sends signals to explicitly owned Unix process groups.
package processgroup

import (
	"errors"
	"fmt"
	"syscall"

	"golang.org/x/sys/unix"
)

const darwinProcessZombie int8 = 5 // SZOMB in sys/proc.h.

// Signal targets a positive owned group ID. Absent or zombie-only groups
// return ESRCH so lifecycle owners can apply their existing completion semantics.
func Signal(groupID int, signal syscall.Signal) error {
	if groupID <= 1 {
		return syscall.EINVAL
	}
	err := syscall.Kill(-groupID, signal)
	if err != syscall.EPERM {
		return err
	}
	return classifyDarwinGroupPermissionError(groupID)
}

func classifyDarwinGroupPermissionError(groupID int) error {
	members, err := unix.SysctlKinfoProcSlice("kern.proc.pgrp", groupID)
	if err != nil {
		return errors.Join(syscall.EPERM, fmt.Errorf("inspect process group %d: %w", groupID, err))
	}
	for _, member := range members {
		if member.Proc.P_stat != darwinProcessZombie {
			return syscall.EPERM
		}
	}
	// Darwin excludes zombies from killpg's eligible members, returning EPERM
	// for a group with no live targets. No member here can execute more work.
	return syscall.ESRCH
}
