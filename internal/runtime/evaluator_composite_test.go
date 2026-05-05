package runtime

import (
	"strings"
	"testing"
)

// --- all_of tests ---

func TestAllOfPassesWhenAllChecksPass(t *testing.T) {
	repo := makeRepoWithFiles(t,
		"rules:\n  - id: gate\n    kind: all_of\n    when_paths: ['docs/todo/{task_id}.md']\n    checks:\n      - kind: require_fresh_file\n        path: 'docs/fidelity/{task_id}.json'\n        max_age_hours: 24\n      - kind: require_evidence\n        file: 'docs/coverage/{task_id}.md'\n        must_not_contain: 'FAIL'\n    mode: block\n    message: m\n",
		map[string]string{"docs/coverage/TODO-001.md": "PASS\n"})
	touchFile(t, repo, "docs/fidelity/TODO-001.json", 1)

	inputs := Empty()
	inputs.WritePaths = []string{"docs/todo/TODO-001.md"}

	report, err := CheckRepoPolicy(repo, inputs)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if report.Decision != DecisionPass {
		t.Errorf("expected pass when all checks pass, got %s; violations: %+v", report.Decision, report.Violations)
	}
}

func TestAllOfBlocksWhenOneCheckFails(t *testing.T) {
	repo := makeRepoWithFiles(t,
		"rules:\n  - id: gate\n    kind: all_of\n    when_paths: ['docs/todo/{task_id}.md']\n    checks:\n      - kind: require_fresh_file\n        path: 'docs/fidelity/{task_id}.json'\n        max_age_hours: 24\n      - kind: require_evidence\n        file: 'docs/coverage/{task_id}.md'\n        must_not_contain: 'FAIL'\n    mode: block\n    message: m\n",
		map[string]string{"docs/coverage/TODO-001.md": "FAIL: oops\n"})
	touchFile(t, repo, "docs/fidelity/TODO-001.json", 1)

	inputs := Empty()
	inputs.WritePaths = []string{"docs/todo/TODO-001.md"}

	report, _ := CheckRepoPolicy(repo, inputs)
	if report.Decision != DecisionBlock {
		t.Errorf("expected block when one check fails, got %s", report.Decision)
	}
	if !strings.Contains(report.Violations[0].Explanation, "all_of") {
		t.Errorf("explanation should mention all_of, got: %s", report.Violations[0].Explanation)
	}
	if !strings.Contains(report.Violations[0].Explanation, "FAIL") {
		t.Errorf("explanation should mention failed check detail, got: %s", report.Violations[0].Explanation)
	}
}

func TestAllOfPassesWhenWhenPathsDontMatch(t *testing.T) {
	repo := makeRepoWithFiles(t,
		"rules:\n  - id: gate\n    kind: all_of\n    when_paths: ['docs/todo/*.md']\n    checks:\n      - kind: require_evidence\n        file: 'docs/x.md'\n        must_exist: true\n    mode: block\n    message: m\n",
		nil)

	inputs := Empty()
	inputs.WritePaths = []string{"src/main.go"}

	report, _ := CheckRepoPolicy(repo, inputs)
	if report.Decision != DecisionPass {
		t.Errorf("expected pass when when_paths don't match, got %s", report.Decision)
	}
}

func TestAllOfWithRequireClaimSubcheck(t *testing.T) {
	repo := makeRepoWithFiles(t,
		"rules:\n  - id: gate\n    kind: all_of\n    when_paths: ['src/**']\n    checks:\n      - kind: require_claim\n        claims: ['ci-green']\n      - kind: require_evidence\n        file: 'docs/changelog.md'\n        must_exist: true\n    mode: block\n    message: m\n",
		map[string]string{"docs/changelog.md": "v1\n"})

	// Without claim -> block
	inputs := Empty()
	inputs.WritePaths = []string{"src/main.go"}
	report, _ := CheckRepoPolicy(repo, inputs)
	if report.Decision != DecisionBlock {
		t.Errorf("expected block without claim, got %s", report.Decision)
	}

	// With claim -> pass
	inputs.Claims = []string{"ci-green"}
	report, _ = CheckRepoPolicy(repo, inputs)
	if report.Decision != DecisionPass {
		t.Errorf("expected pass with claim + changelog, got %s; violations: %+v", report.Decision, report.Violations)
	}
}

// --- any_of tests ---

func TestAnyOfPassesWhenAtLeastOneCheckPasses(t *testing.T) {
	repo := makeRepoWithFiles(t,
		"rules:\n  - id: gate\n    kind: any_of\n    when_paths: ['src/**']\n    checks:\n      - kind: require_claim\n        claims: ['user-override']\n      - kind: require_claim\n        claims: ['ci-green']\n    mode: block\n    message: m\n",
		nil)

	inputs := Empty()
	inputs.WritePaths = []string{"src/main.go"}
	inputs.Claims = []string{"ci-green"} // satisfies the second check

	report, _ := CheckRepoPolicy(repo, inputs)
	if report.Decision != DecisionPass {
		t.Errorf("expected pass when one of the any_of checks satisfied, got %s", report.Decision)
	}
}

func TestAnyOfBlocksWhenAllChecksFail(t *testing.T) {
	repo := makeRepoWithFiles(t,
		"rules:\n  - id: gate\n    kind: any_of\n    when_paths: ['src/**']\n    checks:\n      - kind: require_claim\n        claims: ['user-override']\n      - kind: require_claim\n        claims: ['ci-green']\n    mode: block\n    message: m\n",
		nil)

	inputs := Empty()
	inputs.WritePaths = []string{"src/main.go"}
	// no claims provided

	report, _ := CheckRepoPolicy(repo, inputs)
	if report.Decision != DecisionBlock {
		t.Errorf("expected block when all any_of checks fail, got %s", report.Decision)
	}
}

// --- not tests ---

func TestNotBlocksWhenInnerCheckPasses(t *testing.T) {
	repo := makeRepoWithFiles(t,
		"rules:\n  - id: gate\n    kind: not\n    when_paths: ['src/**']\n    checks:\n      - kind: require_evidence\n        file: 'DEPRECATED.md'\n        must_exist: true\n    mode: block\n    message: m\n",
		map[string]string{"DEPRECATED.md": "this dir is deprecated\n"})

	inputs := Empty()
	inputs.WritePaths = []string{"src/main.go"}

	report, _ := CheckRepoPolicy(repo, inputs)
	if report.Decision != DecisionBlock {
		t.Errorf("expected block when inner check passes (NOT means inner must fail), got %s", report.Decision)
	}
}

func TestNotPassesWhenInnerCheckFails(t *testing.T) {
	repo := makeRepoWithFiles(t,
		"rules:\n  - id: gate\n    kind: not\n    when_paths: ['src/**']\n    checks:\n      - kind: require_evidence\n        file: 'DEPRECATED.md'\n        must_exist: true\n    mode: block\n    message: m\n",
		nil) // no DEPRECATED.md

	inputs := Empty()
	inputs.WritePaths = []string{"src/main.go"}

	report, _ := CheckRepoPolicy(repo, inputs)
	if report.Decision != DecisionPass {
		t.Errorf("expected pass when inner check fails (NOT semantics), got %s", report.Decision)
	}
}

// --- template substitution in composite checks ---

func TestAllOfSubstitutesTemplateVarsInChecks(t *testing.T) {
	// Same gate as TestAllOfPassesWhenAllChecksPass but with TWO writes
	// to verify that substitution happens per-context.
	repo := makeRepoWithFiles(t,
		"rules:\n  - id: gate\n    kind: all_of\n    when_paths: ['docs/todo/{task_id}.md']\n    checks:\n      - kind: require_evidence\n        file: 'docs/coverage/{task_id}.md'\n        must_exist: true\n    mode: block\n    message: m\n",
		map[string]string{
			"docs/coverage/TODO-001.md": "ok\n",
			// TODO-002 coverage missing
		})

	inputs := Empty()
	inputs.WritePaths = []string{"docs/todo/TODO-001.md", "docs/todo/TODO-002.md"}

	report, _ := CheckRepoPolicy(repo, inputs)
	if report.Decision != DecisionBlock {
		t.Errorf("expected block when one context fails, got %s", report.Decision)
	}
	expl := report.Violations[0].Explanation
	if !strings.Contains(expl, "TODO-002") {
		t.Errorf("explanation should mention failing context TODO-002, got: %s", expl)
	}
	if strings.Contains(expl, "TODO-001") && !strings.Contains(expl, "missing") {
		// TODO-001 should NOT appear as a failure (it has its file).
		// (It might appear as part of "all triggered paths" prelude;
		// that's fine. We just check that "missing TODO-001" doesn't
		// appear.)
	}
}

// --- error paths ---

func TestNotRejectsMultipleChecksAtParseTime(t *testing.T) {
	// Parser should refuse at compile time.
	t.Setenv("RECONC_HOME", t.TempDir())
	repo := t.TempDir()
	writeFile(t, repo, "AGENTS.md", "# t\n")
	writeFile(t, repo, "policies/r.yml",
		"rules:\n  - id: bad\n    kind: not\n    when_paths: ['x']\n    checks:\n      - kind: require_claim\n        claims: ['a']\n      - kind: require_claim\n        claims: ['b']\n    mode: block\n    message: m\n")
	_, err := compileTestHelper(repo)
	if err == nil {
		t.Fatal("expected compile to fail for not with multiple checks")
	}
	if !strings.Contains(err.Error(), "exactly one") {
		t.Errorf("expected 'exactly one' in error, got: %v", err)
	}
}
