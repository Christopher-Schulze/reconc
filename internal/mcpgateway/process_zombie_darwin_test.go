//go:build darwin

package mcpgateway

import (
	"os/exec"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestUnixProcessBoundaryTreatsZombieOnlyDarwinGroupAsExited(t *testing.T) {
	command := exec.Command("/bin/sh", "-c", "exit 0")
	boundary, err := prepareProcessBoundary(command)
	if err != nil {
		t.Fatal(err)
	}
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := command.Wait(); err != nil {
			t.Errorf("reap fixture: %v", err)
		}
	})
	pid := command.Process.Pid
	waitForBoundaryZombieGroup(t, pid)
	if err := syscall.Kill(-pid, 0); err != syscall.EPERM {
		t.Fatalf("raw probe on zombie-only group = %v, want EPERM", err)
	}
	if exists, err := unixProcessGroupExists(pid); err != nil || exists {
		t.Fatalf("zombie-only group is live: exists=%v err=%v", exists, err)
	}
	if err := boundary.Attach(command.Process); err != nil {
		t.Fatal(err)
	}
	if err := boundary.Reaped(); err != nil {
		t.Fatalf("terminal group caused reap cleanup failure: %v", err)
	}
	if err := boundary.Close(); err != nil {
		t.Fatal(err)
	}
}

func waitForBoundaryZombieGroup(t *testing.T, pid int) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for {
		members, err := unix.SysctlKinfoProcSlice("kern.proc.pgrp", pid)
		if err != nil {
			t.Fatal(err)
		}
		if len(members) == 1 && members[0].Proc.P_stat == 5 { // SDK SZOMB.
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("fixture did not enter its unreaped zombie state")
		}
		time.Sleep(time.Millisecond)
	}
}
