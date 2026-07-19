//go:build !windows

package main

import (
	"context"
	"os/exec"
)

func auditTaskStateCommand(ctx context.Context, path string) *exec.Cmd {
	return exec.CommandContext(ctx, path, "task-state")
}
