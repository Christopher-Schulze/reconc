//go:build darwin

package processgroup

import (
	"errors"
	"os/exec"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestSignalAcceptsDarwinGroupExitTransition(t *testing.T) {
	for cycle := 0; cycle < 100; cycle++ {
		verifyDarwinGroupExitCleanup(t)
	}
}

func verifyDarwinGroupExitCleanup(t *testing.T) {
	t.Helper()
	command := exec.Command("/bin/sh", "-c", "sleep 30 & wait")
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	pid := command.Process.Pid
	reaped := false
	defer func() {
		if err := Signal(pid, syscall.SIGKILL); err != nil && err != syscall.ESRCH {
			t.Errorf("cleanup owned fixture group: %v", err)
		}
		if !reaped {
			if err := command.Wait(); err != nil {
				var exitError *exec.ExitError
				if !errors.As(err, &exitError) {
					t.Errorf("reap fixture: %v", err)
				}
			}
		}
	}()
	waitForDarwinChildGroup(t, pid)
	if err := syscall.Kill(-pid, syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	var exitError *exec.ExitError
	err := command.Wait()
	reaped = true
	if !errors.As(err, &exitError) {
		t.Fatalf("TERM parent outcome: %v", err)
	}
	if err := Signal(pid, syscall.SIGKILL); err != nil && err != syscall.ESRCH {
		t.Fatalf("terminal group became a permission failure: %v", err)
	}
}

func waitForDarwinChildGroup(t *testing.T, pid int) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for {
		members, err := unix.SysctlKinfoProcSlice("kern.proc.pgrp", pid)
		if err != nil {
			t.Fatal(err)
		}
		if len(members) >= 2 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("child did not join the owned native process group")
		}
		time.Sleep(time.Millisecond)
	}
}
