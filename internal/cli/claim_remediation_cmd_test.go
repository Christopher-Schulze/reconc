package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"reconc.dev/reconc/internal/runtime"
	"reconc.dev/reconc/internal/runtime/agentsession"
)

func TestFixClaimActionFollowsExecutionContext(t *testing.T) {
	repo := makeCheckRepo(t, `rules:
  - id: claim-gate
    kind: require_claim
    when_paths: ['src/**']
    claims: ['ci-green']
    mode: block
    message: claim required
`)
	if err := os.MkdirAll(filepath.Join(repo, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "src", "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	err := Run([]string{"fix", repo, "--write", "src/main.go", "--json"}, "test", &stdout, &stderr)
	if err == nil || ExitCode(err) != 2 {
		t.Fatalf("standalone fix: err=%v code=%d stderr=%s", err, ExitCode(err), stderr.String())
	}
	assertClaimArgv(t, stdout.Bytes(), []string{"reconc", "check", "--claim", "ci-green"})

	if result := agentsession.RunSessionStart(repo, []byte(`{"session_id":"fix-claim"}`)); result.ExitCode != 0 {
		t.Fatalf("session start: %+v", result)
	}
	stdout.Reset()
	stderr.Reset()
	err = Run([]string{"fix", repo, "--write", "src/main.go", "--json"}, "test", &stdout, &stderr)
	if err == nil || ExitCode(err) != 2 {
		t.Fatalf("session fix: err=%v code=%d stderr=%s", err, ExitCode(err), stderr.String())
	}
	assertClaimArgv(t, stdout.Bytes(), []string{"reconc", "hook", "claim", repo, "ci-green", "--session", "fix-claim"})
}

func assertClaimArgv(t *testing.T, body []byte, want []string) {
	t.Helper()
	var plan runtime.FixPlan
	if err := json.Unmarshal(body, &plan); err != nil {
		t.Fatalf("decode fix plan: %v\n%s", err, body)
	}
	if len(plan.Remediations) == 0 || len(plan.Remediations[0].Actions) == 0 {
		t.Fatalf("missing claim action: %#v", plan.Remediations)
	}
	got := plan.Remediations[0].Actions[0].Argv
	if len(got) != len(want) {
		t.Fatalf("argv=%v want %v", got, want)
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("argv[%d]=%q want %q in %v", index, got[index], want[index], got)
		}
	}
	if !strings.Contains(plan.Remediations[0].RecommendedAction, "ci-green") {
		t.Fatalf("recommended action lost claim identity: %s", plan.Remediations[0].RecommendedAction)
	}
}
