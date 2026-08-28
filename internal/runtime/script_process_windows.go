//go:build windows

package runtime

import (
	"context"
	"os"
	"os/exec"
	"time"
)

func configureScriptProcess(cmd *exec.Cmd, killGrace time.Duration) {
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return os.ErrProcessDone
		}
		return cmd.Process.Kill()
	}
	cmd.WaitDelay = killGrace
}

func monitorScriptProcess(context.Context, int, <-chan struct{}, time.Duration) {}
