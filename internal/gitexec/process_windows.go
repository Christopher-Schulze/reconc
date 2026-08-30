//go:build windows

package gitexec

import (
	"os"
	"os/exec"
)

func configureGitCommand(command *exec.Cmd) {
	command.Cancel = func() error {
		if command.Process == nil {
			return os.ErrProcessDone
		}
		return command.Process.Kill()
	}
	command.WaitDelay = gitCancellationWait
}
