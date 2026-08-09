package main

import (
	"errors"
	"os"
	"strings"
	"testing"
	"time"
)

// ruleScriptBudget mirrors the timeout_sec a scaffolded repository grants the
// audit script. The inner scan budget must stay below it so the inner deadline
// fires first and produces an explanatory message instead of the rule killing
// the script with no diagnosis.
const ruleScriptBudget = 30 * time.Second

// TestRunAuditCommandClassifiesBudgetExpiry is the failable core of the repo
// scan budget fix: before it, an expired deadline surfaced only as the kernel's
// "signal: killed", indistinguishable from a crashed or missing binary.
func TestRunAuditCommandClassifiesBudgetExpiry(t *testing.T) {
	_, err := runAuditCommand(100*time.Millisecond, "sh", "-c", "sleep 5")
	if err == nil {
		t.Fatal("expired budget must return an error")
	}
	if !errors.Is(err, errAuditCommandTimeout) {
		t.Fatalf("expired budget must be classified as a timeout, got %v", err)
	}
	if !strings.Contains(err.Error(), "100ms") {
		t.Fatalf("timeout error must name the budget it exceeded, got %v", err)
	}
}

// TestRunAuditCommandDoesNotMisclassifyRealFailure pins the other direction: a
// command that genuinely fails inside its budget must never be excused as a
// timeout, or a broken git would silently stop being a finding.
func TestRunAuditCommandDoesNotMisclassifyRealFailure(t *testing.T) {
	_, err := runAuditCommand(10*time.Second, "sh", "-c", "exit 3")
	if err == nil {
		t.Fatal("non-zero exit must return an error")
	}
	if errors.Is(err, errAuditCommandTimeout) {
		t.Fatalf("non-zero exit inside budget must not be classified as a timeout, got %v", err)
	}
}

func TestRunAuditCommandReturnsOutputOnSuccess(t *testing.T) {
	out, err := runAuditCommand(10*time.Second, "sh", "-c", "printf audit-ok")
	if err != nil {
		t.Fatalf("fast command must succeed, got %v", err)
	}
	if strings.TrimSpace(string(out)) != "audit-ok" {
		t.Fatalf("combined output = %q, want audit-ok", string(out))
	}
}

func TestBoundedAuditOutputRejectsExcessOutput(t *testing.T) {
	var output boundedAuditOutput
	payload := []byte(strings.Repeat("x", maxAuditCommandOutput+1))
	written, err := output.Write(payload)
	if err != nil || written != len(payload) {
		t.Fatalf("Write() = %d, %v", written, err)
	}
	if !output.truncated || len(output.bytes()) != maxAuditCommandOutput {
		t.Fatalf("bounded output length=%d truncated=%v", len(output.bytes()), output.truncated)
	}
}

// TestRepoScanTimeoutFailureExplainsItself guards the message against decaying
// back into an opaque refusal.
func TestRepoScanTimeoutFailureExplainsItself(t *testing.T) {
	_, err := runAuditCommand(50*time.Millisecond, "sh", "-c", "sleep 5")
	if err == nil {
		t.Fatal("expired budget must return an error")
	}
	message := repoScanTimeoutFailure("git clean -nd", err)
	for _, phrase := range []string{
		"git clean -nd",
		"scan budget",
		"scan timeout, not an untracked-content violation",
		"nothing was verified",
		"repoScanCommandTimeout",
	} {
		if !strings.Contains(message, phrase) {
			t.Fatalf("timeout failure %q is missing %q", message, phrase)
		}
	}
	if strings.Contains(message, "\n") {
		t.Fatalf("timeout failure must stay single-line: %q", message)
	}
}

// TestRepoScanTimeoutFailureDoesNotCoachRetry pins the anti-flaky-green rule:
// telling an agent to rerun a gate until it happens to pass is exactly the
// behavior this change removes.
func TestRepoScanTimeoutFailureDoesNotCoachRetry(t *testing.T) {
	_, err := runAuditCommand(50*time.Millisecond, "sh", "-c", "sleep 5")
	if err == nil {
		t.Fatal("expired budget must return an error")
	}
	message := strings.ToLower(repoScanTimeoutFailure("git clean -nd", err))
	for _, forbidden := range []string{"retry", "try again", "rerun", "run it again"} {
		if strings.Contains(message, forbidden) {
			t.Fatalf("timeout failure must not coach retrying a gate, found %q in %q", forbidden, message)
		}
	}
}

// TestRepoScanBudgetFitsBetweenShortAndRuleBudget stops a future edit from
// reintroducing the defect in either direction: a budget at or below the short
// one brings back the random kill on a slow worktree walk, and a budget at or
// above the rule budget hands the kill to the rule, which cannot explain it.
func TestRepoScanBudgetFitsBetweenShortAndRuleBudget(t *testing.T) {
	if repoScanCommandTimeout <= shortAuditCommandTimeout {
		t.Fatalf("repoScanCommandTimeout %s must exceed shortAuditCommandTimeout %s", repoScanCommandTimeout, shortAuditCommandTimeout)
	}
	if repoScanCommandTimeout >= ruleScriptBudget {
		t.Fatalf("repoScanCommandTimeout %s must stay below the %s rule script budget so the inner deadline fires first", repoScanCommandTimeout, ruleScriptBudget)
	}
}

func TestCommandWithTimeoutCancelsProcess(t *testing.T) {
	command, cancel := commandWithTimeout(25*time.Millisecond, os.Args[0], "-test.run=^TestCommandWithTimeoutHelper$")
	if command.WaitDelay != auditProcessWaitDelay {
		t.Fatalf("WaitDelay = %s, want %s", command.WaitDelay, auditProcessWaitDelay)
	}
	command.Env = append(os.Environ(), "RECONC_COMMAND_TIMEOUT_HELPER=1")
	started := time.Now()
	err := command.Run()
	cancel()
	if err == nil {
		t.Fatal("timed command unexpectedly completed")
	}
	if elapsed := time.Since(started); elapsed > 2*time.Second {
		t.Fatalf("timed command took %s, want at most 2s", elapsed)
	}
}

func TestCommandWithTimeoutHelper(t *testing.T) {
	if os.Getenv("RECONC_COMMAND_TIMEOUT_HELPER") != "1" {
		return
	}
	time.Sleep(10 * time.Second)
}
