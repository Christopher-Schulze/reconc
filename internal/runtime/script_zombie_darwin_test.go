//go:build darwin

package runtime

import (
	"context"
	"os/exec"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestMonitorScriptProcessAcceptsZombieOnlyDarwinGroup(t *testing.T) {
	command := exec.Command("/bin/sh", "-c", "exit 0")
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := command.Wait(); err != nil {
			t.Errorf("reap fixture: %v", err)
		}
	})
	pid := command.Process.Pid
	waitForScriptZombieGroup(t, pid)
	if err := syscall.Kill(-pid, syscall.SIGKILL); err != syscall.EPERM {
		t.Fatalf("raw signal on zombie-only group = %v, want EPERM", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	done := make(chan struct{})
	close(done)
	if err := <-monitorScriptProcess(ctx, pid, done, time.Second); err != nil {
		t.Fatalf("exited group became a cleanup failure: %v", err)
	}
}

func waitForScriptZombieGroup(t *testing.T, pid int) {
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
