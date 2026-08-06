//go:build !windows

package runtime

import (
	"errors"
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

func TestClassifyScriptOutcomeIsExhaustiveAndFailClosed(t *testing.T) {
	processErr := errors.New("launch failed")
	tests := []struct {
		name         string
		outcome      ScriptOutcome
		runErr       error
		timeoutSec   int
		want         scriptOutcomeDisposition
		wantDetail   string
		rejectDetail string
	}{
		{name: "pass", outcome: ScriptOutcome{Status: "pass", ExitCode: 0}, want: scriptOutcomePass},
		{name: "block", outcome: ScriptOutcome{Status: "block", ExitCode: 2, Stdout: "blocked\n"}, want: scriptOutcomeBlock, wantDetail: "blocked"},
		{name: "block stderr", outcome: ScriptOutcome{Status: "block", ExitCode: 2, Stderr: "stderr block\n"}, want: scriptOutcomeBlock, wantDetail: "stderr block"},
		{name: "timeout", outcome: ScriptOutcome{Status: "error", ExitCode: -1, Stdout: "untrusted timeout output", TimedOut: true}, timeoutSec: 1, want: scriptOutcomeError, wantDetail: "timed out after 1s", rejectDetail: "untrusted"},
		{name: "process error", outcome: ScriptOutcome{Status: "error", ExitCode: -1}, runErr: processErr, want: scriptOutcomeError, wantDetail: "launch failed"},
		{name: "error without go error", outcome: ScriptOutcome{Status: "error", ExitCode: -1}, want: scriptOutcomeError, wantDetail: "returned error status with exit code -1"},
		{name: "invalid status", outcome: ScriptOutcome{Status: "unknown", ExitCode: 0}, want: scriptOutcomeError, wantDetail: `returned invalid status "unknown" with exit code 0`},
		{name: "contradictory pass", outcome: ScriptOutcome{Status: "pass", ExitCode: 2}, want: scriptOutcomeError, wantDetail: "returned pass status with exit code 2"},
		{name: "contradictory block", outcome: ScriptOutcome{Status: "block", ExitCode: 0}, want: scriptOutcomeError, wantDetail: "returned block status with exit code 0"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := classifyScriptOutcome(test.outcome, test.runErr, test.timeoutSec)
			if got.disposition != test.want || got.detail != test.wantDetail {
				t.Fatalf("classification = (%d, %q), want (%d, %q)", got.disposition, got.detail, test.want, test.wantDetail)
			}
			if test.rejectDetail != "" && strings.Contains(got.detail, test.rejectDetail) {
				t.Fatalf("classification leaked rejected detail %q: %q", test.rejectDetail, got.detail)
			}
		})
	}
}

func TestRunScriptTimeoutKillsProcessGroup(t *testing.T) {
	repo := t.TempDir()
	marker := filepath.Join(repo, "child-survived")
	writeScript(t, repo, ".reconc/scripts/spawn-child.sh",
		"#!/bin/sh\n(sleep 3; echo survived > child-survived) &\nwait\n")
	out, err := RunScript(repo, ".reconc/scripts/spawn-child.sh", nil, ScriptInput{}, 1, 1)
	if err != nil {
		t.Fatalf("unexpected error from timeout: %v", err)
	}
	if !out.TimedOut {
		t.Fatal("expected TimedOut=true")
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("timeout must kill child process group; marker exists or stat failed: %v", err)
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

func TestEvalRequireScriptTimeoutBlocksWithConfiguredDurationAndPath(t *testing.T) {
	t.Setenv("RECONC_HOME", t.TempDir())
	repo := t.TempDir()
	writeFile(t, repo, "AGENTS.md", "# t\n")
	writeScript(t, repo, ".reconc/scripts/timeout.sh", "#!/bin/sh\nprintf 'untrusted timeout output\\n'\nsleep 5\n")
	writeFile(t, repo, "policies/r.yml",
		"rules:\n  - id: timeout-gate\n    kind: require_script\n    when_paths: ['src/**']\n    script: '.reconc/scripts/timeout.sh'\n    timeout_sec: 1\n    kill_timeout_sec: 1\n    mode: block\n    message: m\n")
	if _, err := compileTestHelper(repo); err != nil {
		t.Fatal(err)
	}

	inputs := Empty()
	inputs.WritePaths = []string{"src/main.go"}
	report, err := CheckRepoPolicy(repo, inputs)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if report.Decision != DecisionBlock || len(report.Violations) != 1 {
		t.Fatalf("timeout must block, got decision=%s violations=%#v", report.Decision, report.Violations)
	}
	explanation := report.Violations[0].Explanation
	for _, want := range []string{"[src/main.go]", "timed out after 1s"} {
		if !strings.Contains(explanation, want) {
			t.Fatalf("timeout explanation missing %q: %s", want, explanation)
		}
	}
	if strings.Contains(explanation, "untrusted timeout output") {
		t.Fatalf("timeout explanation must not depend on script output: %s", explanation)
	}
}

func TestWorkflowAuditBatchTimeoutFallsBackToFailClosedPerRuleChecks(t *testing.T) {
	t.Setenv("RECONC_HOME", t.TempDir())
	repo := t.TempDir()
	writeFile(t, repo, "AGENTS.md", "# t\n")
	writeScript(t, repo, "audits/run-workflow-audit", `#!/bin/sh
if [ "${1:-}" = "--batch-json" ]; then
  sleep 5
  exit 0
fi
printf 'fallback blocked %s\n' "${1:-}"
exit 2
`)
	writeFile(t, repo, "policies/r.yml", `rules:
  - id: audit-a
    kind: require_script
    when_paths: ['src/**']
    script: audits/run-workflow-audit
    args: ['mode-a']
    timeout_sec: 3
    mode: block
    message: a
  - id: audit-b
    kind: require_script
    when_paths: ['src/**']
    script: audits/run-workflow-audit
    args: ['mode-b']
    timeout_sec: 3
    mode: block
    message: b
`)
	if _, err := compileTestHelper(repo); err != nil {
		t.Fatal(err)
	}

	report, err := CheckRepoPolicy(repo, ExecutionInputs{WritePaths: []string{"src/main.go"}})
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if report.Decision != DecisionBlock || len(report.Violations) != 2 {
		t.Fatalf("batch timeout fallback must preserve both blocks, got decision=%s violations=%#v", report.Decision, report.Violations)
	}
	for index, ruleID := range []string{"audit-a", "audit-b"} {
		violation := report.Violations[index]
		if violation.RuleID != ruleID || !strings.Contains(violation.Explanation, "fallback blocked mode-") {
			t.Fatalf("fallback violation %d = %#v", index, violation)
		}
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

func TestEvalRequireScriptTimeoutFailsClosedInsideAllOf(t *testing.T) {
	t.Setenv("RECONC_HOME", t.TempDir())
	repo := t.TempDir()
	writeFile(t, repo, "AGENTS.md", "# t\n")
	writeScript(t, repo, ".reconc/scripts/timeout.sh", "#!/bin/sh\nprintf 'composite timeout output\\n'\nsleep 5\n")
	writeFile(t, repo, "policies/r.yml",
		"rules:\n  - id: gate\n    kind: all_of\n    when_paths: ['src/**']\n    checks:\n      - kind: require_script\n        script: '.reconc/scripts/timeout.sh'\n        timeout_sec: 1\n    mode: block\n    message: m\n")
	if _, err := compileTestHelper(repo); err != nil {
		t.Fatal(err)
	}

	report, err := CheckRepoPolicy(repo, ExecutionInputs{WritePaths: []string{"src/main.go"}})
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if report.Decision != DecisionBlock || len(report.Violations) != 1 {
		t.Fatalf("composite timeout must block, got decision=%s violations=%#v", report.Decision, report.Violations)
	}
	explanation := report.Violations[0].Explanation
	if !strings.Contains(explanation, "timed out after 1s") || strings.Contains(explanation, "composite timeout output") {
		t.Fatalf("unexpected composite timeout explanation: %s", explanation)
	}
}
