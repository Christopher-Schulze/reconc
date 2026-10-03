//go:build darwin

package processgroup

import (
	"syscall"
	"testing"

	"golang.org/x/sys/unix"
)

func TestDarwinGroupPermissionErrorKeepsLiveGroup(t *testing.T) {
	groupID := syscall.Getpgrp()
	members, err := unix.SysctlKinfoProcSlice("kern.proc.pgrp", groupID)
	if err != nil || len(members) == 0 {
		t.Fatalf("inspect real test group: members=%d err=%v", len(members), err)
	}
	if err := classifyDarwinGroupPermissionError(groupID); err != syscall.EPERM {
		t.Fatalf("live test group lost its permission error: %v", err)
	}
}
