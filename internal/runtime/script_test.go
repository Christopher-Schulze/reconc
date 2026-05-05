//go:build !windows

package runtime

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeScript creates an executable shell script under repo at the
// given relative path. Helper for require_script tests.
func writeScript(t *testing.T, repo, rel, content string) {
	t.Helper()
	full := filepath.Join(repo, rel)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(full, []byte(content), 0o755); err != nil {
		t.Fatalf("write %s: %v", rel, err)
	}
}

func TestRunScriptExitZeroIsPass(t *testing.T) {
	repo := t.TempDir()
	writeScript(t, repo, ".reconc/scripts/ok.sh", "#!/bin/sh\nexit 0\n")
	out, err := RunScript(repo, ".reconc/scripts/ok.sh", nil, ScriptInput{}, 0, 0)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if out.Status != "pass" {
		t.Errorf("expected pass, got %s", out.Status)
	}
	if out.ExitCode != 0 {
		t.Errorf("expected exit 0, got %d", out.ExitCode)
	}
}

func TestRunScriptExitTwoIsBlock(t *testing.T) {
	repo := t.TempDir()
	writeScript(t, repo, ".reconc/scripts/block.sh", "#!/bin/sh\necho 'blocking output'\nexit 2\n")
	out, err := RunScript(repo, ".reconc/scripts/block.sh", nil, ScriptInput{}, 0, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.Status != "block" {
		t.Errorf("expected block, got %s", out.Status)
	}
	if !strings.Contains(out.Stdout, "blocking output") {
		t.Errorf("stdout should be captured, got: %s", out.Stdout)
	}
}

func TestRunScriptExitOneIsError(t *testing.T) {
	repo := t.TempDir()
	writeScript(t, repo, ".reconc/scripts/oops.sh", "#!/bin/sh\necho 'crash' >&2\nexit 1\n")
	_, err := RunScript(repo, ".reconc/scripts/oops.sh", nil, ScriptInput{}, 0, 0)
	if err == nil {
		t.Fatal("expected error for non-0/non-2 exit")
	}
}

func TestRunScriptMissingScript(t *testing.T) {
	repo := t.TempDir()
	_, err := RunScript(repo, ".reconc/scripts/nope.sh", nil, ScriptInput{}, 0, 0)
	if err == nil {
		t.Fatal("expected error for missing script")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("expected 'not found' in error, got: %v", err)
	}
}

func TestRunScriptNotExecutable(t *testing.T) {
	repo := t.TempDir()
	full := filepath.Join(repo, ".reconc/scripts/no-x.sh")
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte("#!/bin/sh\nexit 0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := RunScript(repo, ".reconc/scripts/no-x.sh", nil, ScriptInput{}, 0, 0)
	if err == nil {
		t.Fatal("expected error for non-executable script")
	}
}

func TestRunScriptTimeout(t *testing.T) {
	repo := t.TempDir()
	writeScript(t, repo, ".reconc/scripts/sleep.sh", "#!/bin/sh\nsleep 5\nexit 0\n")
	out, err := RunScript(repo, ".reconc/scripts/sleep.sh", nil, ScriptInput{}, 1, 1)
	if err != nil {
		t.Fatalf("unexpected error from timeout: %v", err)
	}
	if !out.TimedOut {
		t.Error("expected TimedOut=true")
	}
	if out.Status != "error" {
		t.Errorf("expected status=error, got %s", out.Status)
	}
}

func TestRunScriptStdinHasJSONInput(t *testing.T) {
	repo := t.TempDir()
	// Script reads stdin and exits 0 if it contains the rule_id.
	writeScript(t, repo, ".reconc/scripts/check-stdin.sh",
		"#!/bin/sh\ngrep -q '\"rule_id\":\"my-rule\"' && exit 0\nexit 2\n")
	out, _ := RunScript(repo, ".reconc/scripts/check-stdin.sh", nil,
		ScriptInput{RuleID: "my-rule"}, 0, 0)
	if out.Status != "pass" {
		t.Errorf("script should have seen rule_id on stdin, got %s; stdout=%s stderr=%s", out.Status, out.Stdout, out.Stderr)
	}
}

func TestRunScriptArgsArePassed(t *testing.T) {
	repo := t.TempDir()
	// Script exits 0 if first arg == "TODO-001"
	writeScript(t, repo, ".reconc/scripts/check-args.sh",
		"#!/bin/sh\n[ \"$1\" = \"TODO-001\" ] && exit 0\nexit 2\n")
	out, _ := RunScript(repo, ".reconc/scripts/check-args.sh",
		[]string{"TODO-001"}, ScriptInput{}, 0, 0)
	if out.Status != "pass" {
		t.Errorf("expected pass, got %s; stderr=%s", out.Status, out.Stderr)
	}
}

func TestRunScriptOutputCappedAtMaxBytes(t *testing.T) {
	repo := t.TempDir()
	// Print 200KB to stdout (more than the 64KB cap)
	writeScript(t, repo, ".reconc/scripts/spam.sh",
		"#!/bin/sh\nyes data | head -n 50000\nexit 2\n")
	out, _ := RunScript(repo, ".reconc/scripts/spam.sh", nil, ScriptInput{}, 0, 0)
	if len(out.Stdout) > MaxScriptOutputBytes {
		t.Errorf("stdout exceeded cap: %d bytes (max %d)", len(out.Stdout), MaxScriptOutputBytes)
	}
}

func TestRunScriptCwdIsRepoRoot(t *testing.T) {
	repo := t.TempDir()
	if err := os.WriteFile(filepath.Join(repo, "MARKER"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	writeScript(t, repo, ".reconc/scripts/check-cwd.sh",
		"#!/bin/sh\n[ -f MARKER ] && exit 0\nexit 2\n")
	out, _ := RunScript(repo, ".reconc/scripts/check-cwd.sh", nil, ScriptInput{}, 0, 0)
	if out.Status != "pass" {
		t.Errorf("script should have run in repo root, got %s", out.Status)
	}
}

// --- evalRequireScript end-to-end ---

func TestEvalRequireScriptPassWhenScriptExitsZero(t *testing.T) {
	t.Setenv("RECONC_HOME", t.TempDir())
	repo := t.TempDir()
	writeFile(t, repo, "AGENTS.md", "# t\n")
	writeScript(t, repo, ".reconc/scripts/ok.sh", "#!/bin/sh\nexit 0\n")
	writeFile(t, repo, "policies/r.yml",
		"rules:\n  - id: r\n    kind: require_script\n    when_paths: ['src/**']\n    script: '.reconc/scripts/ok.sh'\n    mode: block\n    message: m\n")
	if _, err := compileTestHelper(repo); err != nil {
		t.Fatal(err)
	}

	inputs := Empty()
	inputs.WritePaths = []string{"src/main.go"}
	report, err := CheckRepoPolicy(repo, inputs)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if report.Decision != DecisionPass {
		t.Errorf("expected pass, got %s; violations: %+v", report.Decision, report.Violations)
	}
}

func TestEvalRequireScriptBlockWhenScriptExitsTwo(t *testing.T) {
	t.Setenv("RECONC_HOME", t.TempDir())
	repo := t.TempDir()
	writeFile(t, repo, "AGENTS.md", "# t\n")
	writeScript(t, repo, ".reconc/scripts/block.sh", "#!/bin/sh\necho 'reason: missing thing'\nexit 2\n")
	writeFile(t, repo, "policies/r.yml",
		"rules:\n  - id: r\n    kind: require_script\n    when_paths: ['src/**']\n    script: '.reconc/scripts/block.sh'\n    mode: block\n    message: m\n")
	if _, err := compileTestHelper(repo); err != nil {
		t.Fatal(err)
	}

	inputs := Empty()
	inputs.WritePaths = []string{"src/main.go"}
	report, _ := CheckRepoPolicy(repo, inputs)
	if report.Decision != DecisionBlock {
		t.Errorf("expected block, got %s", report.Decision)
	}
	if !strings.Contains(report.Violations[0].Explanation, "reason: missing thing") {
		t.Errorf("violation should include script stdout, got: %s", report.Violations[0].Explanation)
	}
}

func TestEvalRequireScriptArgsSubstituteTemplateVars(t *testing.T) {
	t.Setenv("RECONC_HOME", t.TempDir())
	repo := t.TempDir()
	writeFile(t, repo, "AGENTS.md", "# t\n")
	// Script exits 0 only for TODO-001, blocks otherwise.
	writeScript(t, repo, ".reconc/scripts/check-todo.sh",
		"#!/bin/sh\n[ \"$1\" = \"TODO-001\" ] && exit 0\necho 'wrong todo'\nexit 2\n")
	writeFile(t, repo, "policies/r.yml",
		"rules:\n  - id: r\n    kind: require_script\n    when_paths: ['docs/todo/{task_id}.md']\n    script: '.reconc/scripts/check-todo.sh'\n    args: ['{task_id}']\n    mode: block\n    message: m\n")
	if _, err := compileTestHelper(repo); err != nil {
		t.Fatal(err)
	}

	// TODO-001 -> pass
	inputs := Empty()
	inputs.WritePaths = []string{"docs/todo/TODO-001.md"}
	report, _ := CheckRepoPolicy(repo, inputs)
	if report.Decision != DecisionPass {
		t.Errorf("expected pass for TODO-001, got %s; violations: %+v", report.Decision, report.Violations)
	}

	// TODO-002 -> block
	inputs.WritePaths = []string{"docs/todo/TODO-002.md"}
	report, _ = CheckRepoPolicy(repo, inputs)
	if report.Decision != DecisionBlock {
		t.Errorf("expected block for TODO-002, got %s", report.Decision)
	}
}

func TestEvalRequireScriptInsideAllOf(t *testing.T) {
	t.Setenv("RECONC_HOME", t.TempDir())
	repo := t.TempDir()
	writeFile(t, repo, "AGENTS.md", "# t\n")
	writeScript(t, repo, ".reconc/scripts/ok.sh", "#!/bin/sh\nexit 0\n")
	writeScript(t, repo, ".reconc/scripts/block.sh", "#!/bin/sh\nexit 2\n")
	writeFile(t, repo, "policies/r.yml",
		"rules:\n  - id: gate\n    kind: all_of\n    when_paths: ['src/**']\n    checks:\n      - kind: require_script\n        script: '.reconc/scripts/ok.sh'\n      - kind: require_script\n        script: '.reconc/scripts/block.sh'\n    mode: block\n    message: m\n")
	if _, err := compileTestHelper(repo); err != nil {
		t.Fatal(err)
	}

	inputs := Empty()
	inputs.WritePaths = []string{"src/main.go"}
	report, _ := CheckRepoPolicy(repo, inputs)
	if report.Decision != DecisionBlock {
		t.Errorf("expected block (one of two scripts blocked), got %s", report.Decision)
	}
}
