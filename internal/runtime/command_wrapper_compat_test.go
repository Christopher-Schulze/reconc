package runtime

import (
	"os"
	"path/filepath"
	"testing"

	"reconc.dev/reconc/internal/policy"
)

// TestNormalizeCommandSemanticsDropsLeadingCdRepoRoot proves agent-recorded
// commands anchored with an explicit cd into the repo root satisfy the literal
// rule form: `cd /abs/repo && X` (optionally rtk-wrapped) matches `X`.
func TestNormalizeCommandSemanticsDropsLeadingCdRepoRoot(t *testing.T) {
	root := "/workspace/repo"
	cases := []struct {
		name     string
		recorded string
		want     string
	}{
		{"cd root and command", "cd /workspace/repo && go test ./...", "go test ./..."},
		{"cd root semicolon", "cd /workspace/repo ; go test ./...", "go test ./..."},
		{"cd root with rtk wrapper", "cd /workspace/repo && rtk go test ./...", "go test ./..."},
		{"cd subdir keeps cd", "cd /workspace/repo/sub && go test ./...", "cd sub && go test ./..."},
		{"cd root or-join is not dropped", "cd /workspace/repo || go test ./...", "cd . || go test ./..."},
		{"cd root pipe-join is not dropped", "cd /workspace/repo | cat", "cd . | cat"},
		{"bare cd root stays", "cd /workspace/repo", "cd ."},
		{"double anchor collapses", "cd /workspace/repo && cd /workspace/repo && go vet ./...", "go vet ./..."},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := normalizeCommandSemantics(tc.recorded, root); got != tc.want {
				t.Fatalf("normalizeCommandSemantics(%q) = %q, want %q", tc.recorded, got, tc.want)
			}
		})
	}
}

// TestMatchingCommandResultsAcceptWrappedAndAnchoredForms locks the portable
// wrapper compatibility contract: verbatim, rtk-wrapped, and cd-root-anchored
// successes must all satisfy the rule.
func TestMatchingCommandResultsAcceptWrappedAndAnchoredForms(t *testing.T) {
	repoRoot := "/workspace/repo"
	rootGate := []string{
		"codebase/scripts/tests/run-root-tests.sh build",
		"codebase/scripts/tests/run-root-tests.sh test",
	}
	harness := []string{"cd tools/reconc/harness/governed && go test ./..."}
	results := []CommandResult{
		{Command: "codebase/scripts/tests/run-root-tests.sh test", Outcome: CommandOutcomeSuccess, EvidenceEpoch: 45},
		{Command: "cd tools/reconc/harness/governed && rtk go test ./...", Outcome: CommandOutcomeSuccess, EvidenceEpoch: 48},
		{Command: "cd /workspace/repo && codebase/scripts/tests/run-root-tests.sh build", Outcome: CommandOutcomeSuccess, EvidenceEpoch: 48},
		{Command: "cd /workspace/repo && rtk go build ./...", Outcome: CommandOutcomeFailure, EvidenceEpoch: 48},
	}
	if got := matchingCommandResultsSince(results, rootGate, CommandOutcomeSuccess, repoRoot, 0, policy.CommandMatchExact); len(got) != 2 {
		t.Fatalf("root gate should match verbatim and cd-anchored successes, got %v", got)
	}
	if got := matchingCommandResultsSince(results, harness, CommandOutcomeSuccess, repoRoot, 40, policy.CommandMatchExact); len(got) != 1 {
		t.Fatalf("harness rule should match the rtk-wrapped success, got %v", got)
	}
	if got := matchingCommandResultsSince(results, []string{"go build ./..."}, CommandOutcomeSuccess, repoRoot, 0, policy.CommandMatchExact); len(got) != 0 {
		t.Fatal("a failed command must never satisfy require_command_success")
	}
}

// TestRelativizeEpochKeysBridgesAbsoluteAndRelativeSpellings proves session
// write epochs recorded under absolute payload paths become visible to
// repo-relative git-path lookups without losing the original keys.
func TestRelativizeEpochKeysBridgesAbsoluteAndRelativeSpellings(t *testing.T) {
	root := t.TempDir()
	if resolved, err := filepath.EvalSymlinks(root); err == nil {
		root = resolved
	}
	inside := filepath.Join(root, "pkg", "a.go")
	spaced := filepath.Join(root, " spaced.go ")
	if err := os.MkdirAll(filepath.Dir(inside), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(inside, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	epochs := map[string]uint64{
		inside:       7,
		spaced:       5,
		"docs/d.md":  3,
		"/elsewhere": 9,
	}
	out := RelativizeEpochKeys(root, epochs)
	if out["pkg/a.go"] != 7 {
		t.Fatalf("relative alias missing: %v", out)
	}
	if out[inside] != 7 {
		t.Fatalf("original absolute key must be kept: %v", out)
	}
	if out[" spaced.go "] != 5 || out[spaced] != 5 {
		t.Fatalf("space-bearing epoch key identity changed: %v", out)
	}
	if out["docs/d.md"] != 3 {
		t.Fatalf("already-relative keys must survive: %v", out)
	}
	if out["/elsewhere"] != 9 {
		t.Fatalf("outside-root keys must be kept untouched: %v", out)
	}
	if got := RelativizeEpochKeys(root, nil); got != nil {
		t.Fatalf("nil epochs must stay nil, got %v", got)
	}
}
