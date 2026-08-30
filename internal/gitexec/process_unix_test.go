//go:build !windows

package gitexec

import (
	"os/exec"
	"syscall"
)

func configureEscapedGitExecDescendant(command *exec.Cmd) {
	command.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}
