package cli

import (
	"bytes"
	"strings"
	"testing"
)

func TestRepositoryOperandSurfacesRejectExtrasBeforeExecution(t *testing.T) {
	t.Setenv("RECONC_HOME", t.TempDir())
	first := t.TempDir()
	second := t.TempDir()
	tests := []struct {
		name string
		args []string
	}{
		{name: "doctor", args: []string{"doctor", first, second}},
		{name: "status", args: []string{"status", first, second}},
		{name: "check", args: []string{"check", first, second}},
		{name: "assert", args: []string{"assert", "rule-id", first, second}},
		{name: "init", args: []string{"init", first, second, "--profile", "minimal"}},
		{name: "refresh", args: []string{"refresh", first, second}},
		{name: "sources", args: []string{"sources", first, second}},
		{name: "ci", args: []string{"ci", first, second, "--staged"}},
		{name: "exec", args: []string{"exec", first, second, "--", "true"}},
		{name: "adopt", args: []string{"adopt", first, second}},
		{name: "extract", args: []string{"extract", first, second}},
		{name: "fix", args: []string{"fix", first, second}},
		{name: "next", args: []string{"next", first, second}},
		{name: "explain", args: []string{"explain", first, second}},
		{name: "why", args: []string{"why", "rule-id", first, second}},
		{name: "can", args: []string{"can", "write", "path", first, second}},
		{name: "done", args: []string{"done", first, second}},
		{name: "proof", args: []string{"proof", first, second}},
		{name: "prune", args: []string{"prune", first, second}},
		{name: "session-briefing", args: []string{"session-briefing", first, second}},
		{name: "context-size", args: []string{"context", "size", first, second}},
		{name: "start", args: []string{"start", first, second}},
		{name: "tui", args: []string{"tui", first, second}},
		{name: "hook-status", args: []string{"hook", "status", first, second}},
		{name: "hook-install", args: []string{"hook", "install", "codex", first, second}},
		{name: "hook-uninstall", args: []string{"hook", "uninstall", "codex", first, second}},
		{name: "hook-sync-scaffold", args: []string{"hook", "sync-scaffold", first, second}},
		{name: "hook-claim", args: []string{"hook", "claim", first, "claim-name", second}},
		{name: "hook-evidence-status", args: []string{"hook", "evidence-status", first, second}},
		{name: "hook-evidence-resolve", args: []string{"hook", "evidence-resolve", first, second, "--token", "token", "--reason", "reason"}},
		{name: "bootstrap-inspect", args: []string{"bootstrap", "inspect", first, second}},
		{name: "bootstrap-plan", args: []string{"bootstrap", "plan", first, second, "--profile", "minimal"}},
		{name: "bootstrap-apply", args: []string{"bootstrap", "apply", first, second, "--profile", "minimal"}},
		{name: "repo-sync-plan", args: []string{"repo", "sync", "plan", first, second}},
		{name: "repo-sync-verify", args: []string{"repo", "sync", "verify", first, second}},
		{name: "repo-sync-recover", args: []string{"repo", "sync", "recover", first, second}},
		{name: "audit-tail", args: []string{"audit", "tail", first, second}},
		{name: "audit-stats", args: []string{"audit", "stats", first, second}},
		{name: "audit-export", args: []string{"audit", "export", first, second}},
		{name: "audit-verify", args: []string{"audit", "verify", first, second}},
		{name: "run-on", args: []string{"run", "on", first, second}},
		{name: "run-off", args: []string{"run", "off", first, second}},
		{name: "run-reset", args: []string{"run", "reset", first, second}},
		{name: "run-status", args: []string{"run", "status", first, second}},
		{name: "run-log", args: []string{"run", "log", first, second}},
		{name: "task-status", args: []string{"task", "status", first, second}},
		{name: "task-validate", args: []string{"task", "validate", first, second}},
		{name: "task-check-done", args: []string{"task", "check-done", first, second}},
		{name: "task-new", args: []string{"task", "new", first, second, "--title", "Title"}},
		{name: "task-claim", args: []string{"task", "claim", "999", first, second}},
		{name: "task-block", args: []string{"task", "block", first, second, "--reason", "reason"}},
		{name: "task-resume", args: []string{"task", "resume", "999", first, second}},
		{name: "task-split", args: []string{"task", "split", first, second, "--children", "001,002"}},
		{name: "task-promote", args: []string{"task", "promote", first, second}},
		{name: "task-archive", args: []string{"task", "archive", first, second}},
		{name: "task-recover", args: []string{"task", "recover", first, second}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var stdout bytes.Buffer
			var stderr bytes.Buffer
			err := Run(test.args, "test-version", &stdout, &stderr)
			if err == nil || !isExtraOperandError(err.Error()) {
				t.Fatalf("extra repository operand was not rejected during parsing: err=%v stdout=%q stderr=%q",
					err, stdout.String(), stderr.String())
			}
		})
	}
}

func isExtraOperandError(message string) bool {
	for _, fragment := range []string{
		"at most one repo",
		"accepts at most one repo",
		"accepts exactly one",
		"unexpected argument",
		"expected at most one repo",
		"too many positional arguments",
	} {
		if strings.Contains(message, fragment) {
			return true
		}
	}
	return false
}
