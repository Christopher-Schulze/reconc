package runtime

import (
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestRunScriptRejectsPathsOutsideRepository drives the shipped RunScript entry
// point with the lexical escape shapes a lockfile can still carry after a
// hand-edited or migrated policy, and requires the boundary refusal instead of
// an execution attempt.
func TestRunScriptRejectsPathsOutsideRepository(t *testing.T) {
	repo := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside.sh")
	if err := os.WriteFile(outside, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, scriptPath := range []string{"", ".", "..", "../outside.sh", outside} {
		outcome, err := RunScript(repo, scriptPath, nil, ScriptInput{}, 1, 1)
		if err == nil {
			t.Fatalf("script path %q must be refused before execution", scriptPath)
		}
		if outcome.Status != "error" {
			t.Fatalf("script path %q must fail closed with error status, got %q", scriptPath, outcome.Status)
		}
	}
}

// TestNormalizedScriptKillTimeoutStaysInRange proves the single conversion
// point cannot hand an overflowing or unbounded grace period to the process
// backends. time.Duration(math.MaxInt) * time.Second wraps negative, which
// would disable the SIGKILL escalation entirely.
func TestNormalizedScriptKillTimeoutStaysInRange(t *testing.T) {
	cases := []struct {
		name           string
		killTimeoutSec int
		want           time.Duration
	}{
		{name: "zero defaults", killTimeoutSec: 0, want: DefaultScriptKillTimeoutSec * time.Second},
		{name: "negative defaults", killTimeoutSec: -1, want: DefaultScriptKillTimeoutSec * time.Second},
		{name: "declared value", killTimeoutSec: 12, want: 12 * time.Second},
		{name: "above cap clamps", killTimeoutSec: MaxScriptKillTimeoutSec + 1, want: MaxScriptKillTimeoutSec * time.Second},
		{name: "overflowing value clamps", killTimeoutSec: math.MaxInt, want: MaxScriptKillTimeoutSec * time.Second},
		{name: "billion seconds clamps", killTimeoutSec: 10_000_000_000, want: MaxScriptKillTimeoutSec * time.Second},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := normalizedScriptKillTimeout(tc.killTimeoutSec)
			if got != tc.want {
				t.Fatalf("normalizedScriptKillTimeout(%d) = %s, want %s", tc.killTimeoutSec, got, tc.want)
			}
			if got <= 0 || got > MaxScriptKillTimeoutSec*time.Second {
				t.Fatalf("normalized grace %s left the enforceable range", got)
			}
		})
	}
}

// TestResolveRepoScriptPathKeepsRepositoryLocalPaths guards the accepted side
// of the containment contract so the escape checks cannot be satisfied by
// refusing everything.
func TestResolveRepoScriptPathKeepsRepositoryLocalPaths(t *testing.T) {
	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, "scripts", "audits"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, scriptPath := range []string{"check.sh", "scripts/check.sh", "scripts/audits/check.sh", "scripts/./check.sh"} {
		resolved, err := resolveRepoScriptPath(repo, scriptPath)
		if err != nil {
			t.Fatalf("repository-local script %q must resolve: %v", scriptPath, err)
		}
		if !strings.HasSuffix(resolved, filepath.FromSlash(filepath.Clean(scriptPath))) {
			t.Fatalf("resolved script %q lost its repository-relative leaf: %s", scriptPath, resolved)
		}
	}
}
