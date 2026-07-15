//go:build windows

package main

import (
	"os/exec"
	"path/filepath"
)

func auditTaskStateCommand(path string) *exec.Cmd {
	return exec.Command("sh", filepath.ToSlash(path), "task-state")
}
