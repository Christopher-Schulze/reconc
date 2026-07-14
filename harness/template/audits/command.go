package main

import (
	"context"
	"os/exec"
	"time"
)

const (
	shortAuditCommandTimeout = 15 * time.Second
	buildAuditCommandTimeout = 2 * time.Minute
	auditProcessWaitDelay    = 2 * time.Second
)

func commandWithTimeout(timeout time.Duration, name string, args ...string) (*exec.Cmd, context.CancelFunc) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	command := exec.CommandContext(ctx, name, args...)
	command.WaitDelay = auditProcessWaitDelay
	return command, cancel
}
