package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"reconc.dev/reconc/internal/compiler"
	"reconc.dev/reconc/internal/completiongate"
	"reconc.dev/reconc/internal/policy"
)

func TestBuildAndRender(t *testing.T) {
	t.Setenv("RECONC_HOME", t.TempDir())
	repo := t.TempDir()
	if err := os.WriteFile(filepath.Join(repo, "AGENTS.md"), []byte("# test\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(repo, "policies"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "policies", "rules.yml"), []byte("rules:\n  - id: deny\n    kind: deny_write\n    paths: ['generated/**']\n    message: no generated writes\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := compiler.CompileRepoPolicy(repo, "test"); err != nil {
		t.Fatalf("compile: %v", err)
	}

	view, err := Build(repo)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if view.LockfileStatus != "fresh" {
		t.Fatalf("expected fresh lockfile, got %q", view.LockfileStatus)
	}
	if view.RuleCount != 1 {
		t.Fatalf("expected one rule, got %d", view.RuleCount)
	}
	if view.Completion == nil || !view.Completion.OK {
		t.Fatalf("expected completion pass, got %+v", view.Completion)
	}
	text := RenderText(view)
	for _, want := range []string{"reconc tui:", "completion: pass", "Sources:", "Rules:", "deny"} {
		if !strings.Contains(text, want) {
			t.Fatalf("rendered TUI missing %q:\n%s", want, text)
		}
	}
}

func TestRenderTextWidthCoversTerminalStates(t *testing.T) {
	largeSources := make([]SourceSummary, 35)
	largeRules := make([]RuleSummary, 35)
	for index := range largeSources {
		largeSources[index] = SourceSummary{Kind: "policy_file", Path: fmt.Sprintf("policies/very-long-source-name-%02d.yml", index)}
		largeRules[index] = RuleSummary{ID: fmt.Sprintf("very-long-rule-identifier-%02d", index), Kind: policy.KindDenyWrite, Mode: policy.ModeBlock}
	}
	tests := []struct {
		name  string
		view  *View
		width int
		want  []string
	}{
		{
			name:  "narrow empty error",
			view:  &View{RepoRoot: "/tmp/not-discovered", LockfileStatus: "not discovered", Sources: []SourceSummary{}, Rules: []RuleSummary{}, Errors: []string{"no policy markers discovered"}, NextAction: "run bootstrap"},
			width: 40,
			want:  []string{"Errors:", "Sources:", "Rules:", "none"},
		},
		{
			name:  "wide pass",
			view:  &View{RepoRoot: "/tmp/repo", Discovered: true, LockfileStatus: "fresh", Completion: &completiongate.Report{OK: true, Decision: "pass", Checks: []completiongate.Check{{ID: "policy", Status: completiongate.StatusPass, Detail: "fresh"}}}, Sources: []SourceSummary{}, Rules: []RuleSummary{}},
			width: 120,
			want:  []string{"completion: pass", "Sources:", "Rules:"},
		},
		{
			name:  "narrow block",
			view:  &View{RepoRoot: "/tmp/repo", Discovered: true, LockfileStatus: "refresh required", Completion: &completiongate.Report{Decision: "block", Checks: []completiongate.Check{{ID: "policy/lockfile", Status: completiongate.StatusFail, Detail: "compiled policy lockfile requires a refresh before completion"}}}, Sources: []SourceSummary{}, Rules: []RuleSummary{}},
			width: 48,
			want:  []string{"completion: block (1 failed)", "Completion blockers:"},
		},
		{
			name:  "large bounded lists",
			view:  &View{RepoRoot: "/tmp/repo", Discovered: true, LockfileStatus: "fresh", Sources: largeSources, SourceCount: len(largeSources), Rules: largeRules, RuleCount: len(largeRules)},
			width: 72,
			want:  []string{"... 5 more source(s)", "... 5 more rule(s)"},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			text := RenderTextWidth(test.view, test.width)
			if strings.Contains(text, "\x1b[") {
				t.Fatal("TUI output contains ANSI control sequences")
			}
			for _, line := range strings.Split(strings.TrimSuffix(text, "\n"), "\n") {
				if len([]rune(line)) > test.width {
					t.Fatalf("line exceeds width %d: %q", test.width, line)
				}
			}
			for _, want := range test.want {
				if !strings.Contains(text, want) {
					t.Fatalf("render missing %q:\n%s", want, text)
				}
			}
		})
	}
}

func TestBuildRejectsSchemaDriftWithoutWriting(t *testing.T) {
	t.Setenv("RECONC_HOME", t.TempDir())
	repo := t.TempDir()
	if err := os.WriteFile(filepath.Join(repo, "AGENTS.md"), []byte("# test\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := compiler.CompileRepoPolicy(repo, "test"); err != nil {
		t.Fatalf("compile: %v", err)
	}
	lockPath := filepath.Join(repo, ".reconc", "policy.lock.json")
	data, err := os.ReadFile(lockPath)
	if err != nil {
		t.Fatal(err)
	}
	drifted := []byte(strings.Replace(string(data), compiler.DefaultLockfileSchema, "https://invalid.example/policy-lock/v1", 1))
	if err := os.WriteFile(lockPath, drifted, 0o644); err != nil {
		t.Fatal(err)
	}

	view, err := Build(repo)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if view.LockfileStatus != "refresh required" || !strings.Contains(view.NextAction, "reconc refresh .") {
		t.Fatalf("TUI accepted schema drift: %+v", view)
	}
	if view.Completion == nil || view.Completion.OK {
		t.Fatalf("completion gate accepted schema drift: %+v", view.Completion)
	}
	after, err := os.ReadFile(lockPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(drifted) {
		t.Fatal("TUI modified the schema-drifted lockfile")
	}
}

func TestBuildUndiscoveredUsesCanonicalInitRemediation(t *testing.T) {
	t.Setenv("RECONC_HOME", t.TempDir())
	repo := t.TempDir()

	view, err := Build(repo)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if view.Discovered || view.RepoRoot != repo {
		t.Fatalf("undiscovered view = %+v", view)
	}
	if view.NextAction != "run `reconc init "+repo+"`" {
		t.Fatalf("next action = %q", view.NextAction)
	}
}
