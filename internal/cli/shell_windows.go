//go:build windows

package cli

import "os/exec"

func shellCommand(command string) *exec.Cmd {
	return exec.Command("cmd.exe", "/S", "/C", command)
}
