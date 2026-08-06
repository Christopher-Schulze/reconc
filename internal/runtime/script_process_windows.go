//go:build windows

package runtime

import (
	"context"
	"os"
	"os/exec"
	"time"
)

func configureScriptProcess(_ context.Context, cmd *exec.Cmd, _ <-chan struct{}, killGrace time.Duration) {
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return os.ErrProcessDone
		}
		return cmd.Process.Kill()
	}
	cmd.WaitDelay = killGrace
}
