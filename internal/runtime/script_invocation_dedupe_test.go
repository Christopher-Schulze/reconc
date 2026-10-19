//go:build !windows

package runtime

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// countingScriptRepo installs a script that records every invocation's first
// argument (or "-") in the repository-root counter file, because scripts run
// from a private copy with the repository root as working directory.
func countingScriptRepo(t *testing.T, rulesYAML, exitStatus string) (string, string) {
	t.Helper()
	repo := makeRepoWithFiles(t, rulesYAML, map[string]string{
		"scripts/count.sh": "#!/bin/sh\nprintf '%s\\n' \"${1:--}\" >> counter\nexit " + exitStatus + "\n",
	})
	if err := os.Chmod(filepath.Join(repo, "scripts", "count.sh"), 0o755); err != nil {
		t.Fatal(err)
	}
	return repo, filepath.Join(repo, "counter")
}

func invocationLines(t *testing.T, counter string) []string {
	t.Helper()
	body, err := os.ReadFile(counter)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	return strings.Split(strings.TrimSuffix(string(body), "\n"), "\n")
}

func TestRequireScriptRunsOnceForPathsSharingItsInputs(t *testing.T) {
	repo, counter := countingScriptRepo(t, "rules:\n  - id: gate\n    kind: require_script\n    when_paths: ['src/**']\n    script: 'scripts/count.sh'\n    args: ['audit']\n    mode: block\n    message: m\n", "2")
	inputs := Empty()
	inputs.WritePaths = []string{"src/a.go", "src/b.go", "src/c.go"}
	report, err := CheckRepoPolicy(repo, inputs)
	if err != nil {
		t.Fatal(err)
	}
	if got := invocationLines(t, counter); len(got) != 1 || got[0] != "audit" {
		t.Fatalf("script invocations = %q, want exactly one shared run", got)
	}
	if report.Decision != DecisionBlock || len(report.Violations) != 1 {
		t.Fatalf("decision = %s, violations = %+v", report.Decision, report.Violations)
	}
	explanation := report.Violations[0].Explanation
	for _, want := range []string{"src/a.go", "src/b.go", "src/c.go", "[src/a.go +2 more paths] script scripts/count.sh blocked"} {
		if !strings.Contains(explanation, want) {
			t.Fatalf("explanation lacks %q:\n%s", want, explanation)
		}
	}
}

func TestRequireScriptRunsOncePerDistinctCapture(t *testing.T) {
	repo, counter := countingScriptRepo(t, "rules:\n  - id: gate\n    kind: require_script\n    when_paths: ['pkg/{name}/**']\n    script: 'scripts/count.sh'\n    args: ['{name}']\n    mode: block\n    message: m\n", "0")
	inputs := Empty()
	inputs.WritePaths = []string{"pkg/a/x.go", "pkg/b/z.go", "pkg/a/y.go"}
	report, err := CheckRepoPolicy(repo, inputs)
	if err != nil {
		t.Fatal(err)
	}
	if report.Decision != DecisionPass {
		t.Fatalf("decision = %s, violations = %+v", report.Decision, report.Violations)
	}
	if got := strings.Join(invocationLines(t, counter), ","); got != "a,b" {
		t.Fatalf("script invocations = %q, want one run per distinct capture in first-match order", got)
	}
}

func TestCompositeScriptSubCheckRunsOncePerSharedInputs(t *testing.T) {
	repo, counter := countingScriptRepo(t, "rules:\n  - id: gate\n    kind: all_of\n    when_paths: ['src/**']\n    checks:\n      - kind: require_script\n        script: 'scripts/count.sh'\n        args: ['composite']\n    mode: block\n    message: m\n", "2")
	inputs := Empty()
	inputs.WritePaths = []string{"src/a.go", "src/b.go", "src/c.go"}
	report, err := CheckRepoPolicy(repo, inputs)
	if err != nil {
		t.Fatal(err)
	}
	if got := invocationLines(t, counter); len(got) != 1 || got[0] != "composite" {
		t.Fatalf("sub-check invocations = %q, want exactly one shared run", got)
	}
	if report.Decision != DecisionBlock || len(report.Violations) != 1 || !strings.Contains(report.Violations[0].Explanation, "3 context(s)") {
		t.Fatalf("every context must keep the shared blocked outcome: %+v", report.Violations)
	}
}
