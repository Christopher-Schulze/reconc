//go:build windows

package main

import (
	"context"
	"os/exec"
	"path/filepath"
)

func auditTaskStateCommand(ctx context.Context, path string) *exec.Cmd {
	return exec.CommandContext(ctx, "sh", filepath.ToSlash(path), "task-state")
}
