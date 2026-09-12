//go:build !windows

package cli

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"syscall"
	"time"
)

func configureLiveHookCaptureProcess(command *exec.Cmd) (int, error) {
	configureHookVerificationProcess(command)
	value := os.Getenv("RECONC_HOOK_VERIFY_HOST_GROUP")
	if value == "" {
		return 0, nil
	}
	group, err := strconv.Atoi(value)
	actual, groupErr := syscall.Getpgid(0)
	if err != nil || groupErr != nil || group <= 1 {
		return 0, fmt.Errorf("capture is outside the owned native host process group")
	}
	ownerGroup, ownerErr := syscall.Getpgid(group)
	if ownerErr != nil || ownerGroup != group {
		return 0, fmt.Errorf("native host no longer owns its process group")
	}
	if actual != group {
		// Hosts may start the hook shell in a separate process group. Join the
		// still-live native owner before starting any policy/runtime children.
		if err := syscall.Setpgid(0, group); err != nil {
			return 0, fmt.Errorf("capture could not join the owned native host process group")
		}
	}
	// Keep wrapper descendants in the same owned group. A wrapper timeout
	// aborts the whole disposable host run rather than leaving it detached.
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true, Pgid: group}
	command.Cancel = func() error { return syscall.Kill(-group, syscall.SIGKILL) }
	return group, nil
}

func configureHookVerificationProcess(command *exec.Cmd) {
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	command.Cancel = func() error {
		if command.Process == nil {
			return os.ErrProcessDone
		}
		if err := syscall.Kill(-command.Process.Pid, syscall.SIGKILL); err != nil {
			if err == syscall.ESRCH {
				return os.ErrProcessDone
			}
			return err
		}
		return nil
	}
	command.WaitDelay = 100 * time.Millisecond
}

func cleanupLiveHookHostProcess(command *exec.Cmd) error {
	if command.Process == nil {
		return nil
	}
	group := command.Process.Pid
	if err := syscall.Kill(-group, syscall.SIGKILL); err != nil && err != syscall.ESRCH {
		return fmt.Errorf("native host process group could not be terminated")
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if err := syscall.Kill(-group, 0); err == syscall.ESRCH {
			return nil
		}
		time.Sleep(5 * time.Millisecond)
	}
	return fmt.Errorf("native host process group remained after cleanup")
}
