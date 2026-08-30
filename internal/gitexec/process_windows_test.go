//go:build windows

package gitexec

import "os/exec"

func configureEscapedGitExecDescendant(*exec.Cmd) {}
