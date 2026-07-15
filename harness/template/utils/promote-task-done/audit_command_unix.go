//go:build !windows

package main

import "os/exec"

func auditTaskStateCommand(path string) *exec.Cmd {
	return exec.Command(path, "task-state")
}
