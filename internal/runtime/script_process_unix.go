//go:build !windows

package runtime

import (
	"context"
	"os"
	"os/exec"
	"syscall"
	"time"
)

func configureScriptProcess(cmd *exec.Cmd, killGrace time.Duration) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	// Cancel sends SIGTERM to the whole process group. A shell script may spawn
	// grandchildren (for example go build -> compile); killing only the shell
	// leaves those children orphaned and can exhaust the host process table.
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return os.ErrProcessDone
		}
		if err := syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM); err != nil {
			if err == syscall.ESRCH {
				return os.ErrProcessDone
			}
			return err
		}
		return nil
	}
	cmd.WaitDelay = killGrace
}

func monitorScriptProcess(ctx context.Context, pid int, done <-chan struct{}, killGrace time.Duration) <-chan error {
	finished := make(chan error, 1)
	go func() {
		defer close(finished)
		select {
		case <-ctx.Done():
			timer := time.NewTimer(killGrace)
			defer timer.Stop()
			select {
			case <-timer.C:
			case <-done:
			}
		case <-done:
			if ctx.Err() == nil {
				return
			}
		}
		// Wait can reap a TERM-sensitive leader while resistant descendants
		// have closed their inherited streams. They still own this group.
		if err := syscall.Kill(-pid, syscall.SIGKILL); err != nil && err != syscall.ESRCH {
			finished <- err
		}
	}()
	return finished
}
