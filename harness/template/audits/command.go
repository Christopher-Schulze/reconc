package main

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"time"
)

const (
	shortAuditCommandTimeout = 15 * time.Second
	// repoScanCommandTimeout covers git calls that walk the entire worktree
	// rather than reading the index. Those walks scale with repository size
	// and spike further under build load, so the short budget kills them at
	// random on a large repository and turns a blocking gate into a coin
	// flip. It stays strictly below the script budget the invoking rule
	// grants, so this budget expires first and the caller can explain the
	// timeout instead of the rule killing the script opaquely.
	repoScanCommandTimeout   = 25 * time.Second
	buildAuditCommandTimeout = 2 * time.Minute
	auditProcessWaitDelay    = 2 * time.Second
)

// errAuditCommandTimeout marks a command that was killed because its own
// budget expired. Callers distinguish it from a genuine command failure so a
// slow scan is never reported as a policy violation.
var errAuditCommandTimeout = errors.New("audit command budget exceeded")

func commandWithTimeout(timeout time.Duration, name string, args ...string) (*exec.Cmd, context.CancelFunc) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	command := exec.CommandContext(ctx, name, args...)
	command.WaitDelay = auditProcessWaitDelay
	return command, cancel
}

// runAuditCommand runs a command under its own budget and reports combined
// output. A budget expiry is wrapped as errAuditCommandTimeout; without this
// the caller only sees the kernel's "signal: killed", which cannot be told
// apart from a crashed or missing binary.
func runAuditCommand(timeout time.Duration, name string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	command := exec.CommandContext(ctx, name, args...)
	command.WaitDelay = auditProcessWaitDelay
	out, err := command.CombinedOutput()
	if err != nil && ctx.Err() != nil && errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return out, fmt.Errorf("%w after %s: %v", errAuditCommandTimeout, timeout, err)
	}
	return out, err
}

// repoScanTimeoutFailure explains a worktree walk that ran out of budget. It is
// deliberately not a violation message: nothing untracked was found, the scan
// never finished. It also deliberately does not suggest retrying, which would
// train agents to spin a gate until it happens to pass.
func repoScanTimeoutFailure(command string, err error) string {
	return fmt.Sprintf(
		"%s could not finish inside its %s scan budget (%v). This is a scan timeout, not an untracked-content violation: the walk never completed, so nothing was verified. A worktree walk this slow usually means a large untracked or unignored tree. Run `%s` directly to see what it traverses, then ignore the offending tree, or raise repoScanCommandTimeout together with the invoking rule's timeout_sec.",
		command, repoScanCommandTimeout, err, command)
}
