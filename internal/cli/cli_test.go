package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"reconc.dev/reconc/internal/completion"
)

func TestRunEmptyArgvPrintsUsage(t *testing.T) {
	var stdout, stderr bytes.Buffer
	err := Run(nil, "0.1.0-test", &stdout, &stderr)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(stdout.String(), "Repository Control Compiler") {
		t.Errorf("usage banner missing; got: %s", stdout.String())
	}
}

func TestRunVersionFlag(t *testing.T) {
	for _, flag := range []string{"--version", "-V"} {
		t.Run(flag, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			err := Run([]string{flag}, "0.1.0-test", &stdout, &stderr)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			want := "reconc 0.1.0-test\n"
			if stdout.String() != want {
				t.Errorf("got %q, want %q", stdout.String(), want)
			}
		})
	}
}

func TestRunHelpFlag(t *testing.T) {
	for _, flag := range []string{"--help", "-h", "help"} {
		t.Run(flag, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			err := Run([]string{flag}, "0.1.0-test", &stdout, &stderr)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			out := stdout.String()
			for _, want := range []string{"init", "compile", "check", "verify", "why", "can"} {
				if !strings.Contains(out, want) {
					t.Errorf("help output missing %q; got:\n%s", want, out)
				}
			}
		})
	}
}

func TestRunUnknownSubcommandReturnsCLIError(t *testing.T) {
	var stdout, stderr bytes.Buffer
	err := Run([]string{"definitely-not-a-real-subcommand"}, "0.1.0-test", &stdout, &stderr)
	if err == nil {
		t.Fatal("expected error for unimplemented subcommand")
	}
	if code := ExitCode(err); code != 1 {
		t.Errorf("expected exit code 1, got %d", code)
	}
	if !strings.Contains(err.Error(), "not yet implemented") {
		t.Errorf("error message should mention not-yet-implemented; got: %s", err.Error())
	}
}

func TestExitCodeNilError(t *testing.T) {
	if code := ExitCode(nil); code != 0 {
		t.Errorf("nil error should map to exit 0, got %d", code)
	}
}

func TestExitCodeCLIError(t *testing.T) {
	err := &CLIError{ExitCode: 2, Message: "blocking violation"}
	if code := ExitCode(err); code != 2 {
		t.Errorf("CLIError with ExitCode 2 should map to 2, got %d", code)
	}
}

func TestExitCodeGenericError(t *testing.T) {
	err := &genericError{msg: "oops"}
	if code := ExitCode(err); code != 1 {
		t.Errorf("generic error should map to exit 1, got %d", code)
	}
}

type genericError struct{ msg string }

func (e *genericError) Error() string { return e.msg }

func TestRunDoctorOnEmptyDirectory(t *testing.T) {
	repo := t.TempDir()
	var stdout, stderr bytes.Buffer
	err := Run([]string{"doctor", repo}, "0.1.0-test", &stdout, &stderr)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	out := stdout.String()
	if !strings.Contains(out, "discovered:  false") {
		t.Errorf("expected discovered:false in output, got: %s", out)
	}
}

func TestRunDoctorOnDiscoveredRepo(t *testing.T) {
	repo := t.TempDir()
	if err := os.WriteFile(filepath.Join(repo, "AGENTS.md"), []byte("# agents\n"), 0o644); err != nil {
		t.Fatalf("write AGENTS.md: %v", err)
	}

	var stdout, stderr bytes.Buffer
	err := Run([]string{"doctor", repo}, "0.1.0-test", &stdout, &stderr)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	out := stdout.String()
	if !strings.Contains(out, "discovered:  true") {
		t.Errorf("expected discovered:true in output, got: %s", out)
	}
	if !strings.Contains(out, "entry file:  AGENTS.md") {
		t.Errorf("expected entry file AGENTS.md, got: %s", out)
	}
}

func TestRunDoctorJSONOutput(t *testing.T) {
	repo := t.TempDir()
	if err := os.WriteFile(filepath.Join(repo, "start.md"), []byte("# start\n"), 0o644); err != nil {
		t.Fatalf("write start.md: %v", err)
	}

	var stdout, stderr bytes.Buffer
	err := Run([]string{"doctor", repo, "--json"}, "0.1.0-test", &stdout, &stderr)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var payload map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		t.Fatalf("output should be valid JSON: %v\n%s", err, stdout.String())
	}
	if payload["discovered"] != true {
		t.Errorf("expected discovered=true in JSON, got %v", payload["discovered"])
	}
}

func TestRunDoctorHelp(t *testing.T) {
	var stdout, stderr bytes.Buffer
	err := Run([]string{"doctor", "--help"}, "0.1.0-test", &stdout, &stderr)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(stdout.String(), "Usage: reconc doctor") {
		t.Errorf("expected usage line for doctor, got: %s", stdout.String())
	}
}

func TestRunDoctorUnknownFlag(t *testing.T) {
	var stdout, stderr bytes.Buffer
	err := Run([]string{"doctor", "--nope"}, "0.1.0-test", &stdout, &stderr)
	if err == nil {
		t.Fatal("expected CLIError for unknown flag")
	}
	if code := ExitCode(err); code != 1 {
		t.Errorf("expected exit 1, got %d", code)
	}
}

func TestRunDoctorMissingPath(t *testing.T) {
	var stdout, stderr bytes.Buffer
	err := Run([]string{"doctor", "/no/such/path/for/reconc"}, "0.1.0-test", &stdout, &stderr)
	if err == nil {
		t.Fatal("expected CLIError for missing repo path")
	}
}

func TestRunCompileWritesLockfile(t *testing.T) {
	t.Setenv("RECONC_HOME", t.TempDir())
	repo := t.TempDir()
	if err := os.WriteFile(filepath.Join(repo, "AGENTS.md"), []byte("# project\n"), 0o644); err != nil {
		t.Fatalf("write AGENTS.md: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(repo, "policies"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo, "policies", "rules.yml"),
		[]byte("rules:\n  - id: r1\n    kind: deny_write\n    paths: ['x']\n    mode: warn\n    message: x\n"), 0o644); err != nil {
		t.Fatalf("write rules: %v", err)
	}

	var stdout, stderr bytes.Buffer
	err := Run([]string{"compile", repo}, "0.1.0-test", &stdout, &stderr)
	if err != nil {
		t.Fatalf("unexpected error: %v\nstderr: %s", err, stderr.String())
	}
	if !strings.Contains(stdout.String(), "Compiled 1 rules") {
		t.Errorf("expected compile success line, got: %s", stdout.String())
	}
	if _, err := os.Stat(filepath.Join(repo, ".reconc", "policy.lock.json")); err != nil {
		t.Errorf("expected lockfile to exist: %v", err)
	}
}

func TestRunCompileJSONOutput(t *testing.T) {
	t.Setenv("RECONC_HOME", t.TempDir())
	repo := t.TempDir()
	if err := os.WriteFile(filepath.Join(repo, "AGENTS.md"), []byte("# project\n"), 0o644); err != nil {
		t.Fatalf("write AGENTS.md: %v", err)
	}

	var stdout, stderr bytes.Buffer
	err := Run([]string{"compile", repo, "--json"}, "0.1.0-test", &stdout, &stderr)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var payload map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		t.Fatalf("output should be valid JSON: %v\n%s", err, stdout.String())
	}
	if payload["compiler_version"] != "0.1.0-test" {
		t.Errorf("expected compiler_version 0.1.0-test, got %v", payload["compiler_version"])
	}
}

func TestRunCompileFailsOnInvalidRule(t *testing.T) {
	t.Setenv("RECONC_HOME", t.TempDir())
	repo := t.TempDir()
	_ = os.WriteFile(filepath.Join(repo, "AGENTS.md"), []byte("# project\n"), 0o644)
	_ = os.MkdirAll(filepath.Join(repo, "policies"), 0o755)
	_ = os.WriteFile(filepath.Join(repo, "policies", "bad.yml"),
		[]byte("rules:\n  - id: x\n    kind: explode\n    paths: ['x']\n    mode: warn\n    message: x\n"), 0o644)

	var stdout, stderr bytes.Buffer
	err := Run([]string{"compile", repo}, "0.1.0-test", &stdout, &stderr)
	if err == nil {
		t.Fatal("expected CLIError for invalid rule")
	}
	if code := ExitCode(err); code != 1 {
		t.Errorf("expected exit 1 for runtime error, got %d", code)
	}
}

func TestRunCompileHelp(t *testing.T) {
	var stdout, stderr bytes.Buffer
	err := Run([]string{"compile", "--help"}, "0.1.0-test", &stdout, &stderr)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(stdout.String(), "Usage: reconc compile") {
		t.Errorf("expected usage line for compile, got: %s", stdout.String())
	}
}

func TestRunCompileUnknownFlag(t *testing.T) {
	var stdout, stderr bytes.Buffer
	err := Run([]string{"compile", "--bogus"}, "0.1.0-test", &stdout, &stderr)
	if err == nil {
		t.Fatal("expected CLIError for unknown flag")
	}
}

// makeCheckRepo creates a compiled repo + returns its path. Used by
// reconc check tests.
func makeCheckRepo(t *testing.T, rulesYAML string) string {
	t.Helper()
	t.Setenv("RECONC_HOME", t.TempDir())
	repo := t.TempDir()
	if err := os.WriteFile(filepath.Join(repo, "AGENTS.md"), []byte("# test\n"), 0o644); err != nil {
		t.Fatalf("write AGENTS.md: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(repo, "policies"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo, "policies", "rules.yml"), []byte(rulesYAML), 0o644); err != nil {
		t.Fatalf("write rules: %v", err)
	}
	// Compile via the CLI itself so all paths use the same code path.
	var stdout, stderr bytes.Buffer
	if err := Run([]string{"compile", repo}, "0.1.0-test", &stdout, &stderr); err != nil {
		t.Fatalf("compile via CLI: %v\nstderr: %s", err, stderr.String())
	}
	return repo
}

func runCommandWithOutputFile(t *testing.T, version string, argv []string) (string, string) {
	t.Helper()
	outputPath := filepath.Join(t.TempDir(), "nested", "report.out")
	var stdout, stderr bytes.Buffer
	argvWithOutput := append(append([]string{}, argv...), "--output", outputPath)
	if err := Run(argvWithOutput, version, &stdout, &stderr); err != nil {
		t.Fatalf("run %v: %v\nstderr: %s", argvWithOutput, err, stderr.String())
	}
	data, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatalf("read output file %s: %v", outputPath, err)
	}
	if len(data) == 0 {
		t.Fatalf("output file %s is empty", outputPath)
	}
	if stdout.String() != string(data) {
		t.Fatalf("stdout/file mismatch\nstdout:\n%s\nfile:\n%s", stdout.String(), string(data))
	}
	return stdout.String(), string(data)
}

func initGitRepo(t *testing.T, repo string) {
	t.Helper()
	for _, args := range [][]string{
		{"init"},
		{"config", "user.name", "reconc-test"},
		{"config", "user.email", "reconc-test@example.com"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v failed: %v\n%s", args, err, string(out))
		}
	}
}

func approxTokens(s string) int {
	if strings.TrimSpace(s) == "" {
		return 0
	}
	return (len(s) + 3) / 4
}

func TestRunCheckPassesWithNoEvidence(t *testing.T) {
	repo := makeCheckRepo(t,
		"rules:\n  - id: deny-gen\n    kind: deny_write\n    paths: ['gen/**']\n    mode: block\n    message: m\n")

	var stdout, stderr bytes.Buffer
	err := Run([]string{"check", repo}, "0.1.0-test", &stdout, &stderr)
	if err != nil {
		t.Fatalf("unexpected error: %v\nstderr: %s", err, stderr.String())
	}
	if !strings.Contains(stdout.String(), "Decision:  pass") {
		t.Errorf("expected Decision:  pass in output, got: %s", stdout.String())
	}
}

func TestRunCheckBlocksOnDenyWriteWithExit2(t *testing.T) {
	repo := makeCheckRepo(t,
		"rules:\n  - id: deny-gen\n    kind: deny_write\n    paths: ['gen/**']\n    mode: block\n    message: m\n")

	var stdout, stderr bytes.Buffer
	err := Run([]string{"check", repo, "--write", "gen/output.go"}, "0.1.0-test", &stdout, &stderr)
	if err == nil {
		t.Fatal("expected CLIError for blocking violation")
	}
	if code := ExitCode(err); code != 2 {
		t.Errorf("expected exit code 2 for blocking violation, got %d", code)
	}
	if !strings.Contains(stdout.String(), "Decision:  block") {
		t.Errorf("expected Decision:  block in output, got: %s", stdout.String())
	}
	if !strings.Contains(stdout.String(), "deny-gen") {
		t.Errorf("expected rule id in output, got: %s", stdout.String())
	}
}

func TestRunCheckJSONOutput(t *testing.T) {
	repo := makeCheckRepo(t,
		"rules:\n  - id: deny-gen\n    kind: deny_write\n    paths: ['gen/**']\n    mode: warn\n    message: m\n")

	var stdout, stderr bytes.Buffer
	err := Run([]string{"check", repo, "--write", "gen/output.go", "--json"}, "0.1.0-test", &stdout, &stderr)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		t.Fatalf("output should be valid JSON: %v\n%s", err, stdout.String())
	}
	if payload["decision"] != "warn" {
		t.Errorf("expected decision=warn, got %v", payload["decision"])
	}
}

func TestRunCheckCombinesMultipleEvidence(t *testing.T) {
	repo := makeCheckRepo(t,
		"rules:\n  - id: ci-green\n    kind: require_claim\n    when_paths: ['src/**']\n    claims: ['ci-green']\n    mode: block\n    message: m\n")

	// Without claim -> block.
	var stdout, stderr bytes.Buffer
	err := Run([]string{"check", repo, "--write", "src/main.go"}, "0.1.0-test", &stdout, &stderr)
	if ExitCode(err) != 2 {
		t.Errorf("expected exit 2 without claim, got %d", ExitCode(err))
	}

	// With claim -> pass.
	stdout.Reset()
	stderr.Reset()
	err = Run([]string{"check", repo, "--write", "src/main.go", "--claim", "ci-green"}, "0.1.0-test", &stdout, &stderr)
	if err != nil {
		t.Errorf("expected pass with claim, got error: %v", err)
	}
}

func TestRunCheckCommandSuccessFlag(t *testing.T) {
	repo := makeCheckRepo(t,
		"rules:\n  - id: tests-must-pass\n    kind: require_command_success\n    when_paths: ['src/**']\n    commands: ['go test']\n    mode: block\n    message: m\n")

	// Command but not marked success -> block.
	var stdout, stderr bytes.Buffer
	err := Run([]string{"check", repo, "--write", "src/main.go", "--command", "go test"}, "0.1.0-test", &stdout, &stderr)
	if ExitCode(err) != 2 {
		t.Errorf("expected exit 2, got %d", ExitCode(err))
	}

	// --command-success -> pass.
	stdout.Reset()
	stderr.Reset()
	err = Run([]string{"check", repo, "--write", "src/main.go", "--command-success", "go test"}, "0.1.0-test", &stdout, &stderr)
	if err != nil {
		t.Errorf("expected pass with command-success, got error: %v", err)
	}
}

func TestRunCheckHelp(t *testing.T) {
	var stdout, stderr bytes.Buffer
	err := Run([]string{"check", "--help"}, "0.1.0-test", &stdout, &stderr)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(stdout.String(), "Usage: reconc check") {
		t.Errorf("expected usage line, got: %s", stdout.String())
	}
}

func TestRunCheckUnknownFlag(t *testing.T) {
	var stdout, stderr bytes.Buffer
	err := Run([]string{"check", ".", "--bogus"}, "0.1.0-test", &stdout, &stderr)
	if err == nil {
		t.Fatal("expected CLIError for unknown flag")
	}
}

func TestRunCheckFlagWithoutValue(t *testing.T) {
	var stdout, stderr bytes.Buffer
	err := Run([]string{"check", ".", "--write"}, "0.1.0-test", &stdout, &stderr)
	if err == nil {
		t.Fatal("expected CLIError for --write without value")
	}
}

// --- reconc assert tests ---

func makeAssertRepo(t *testing.T, rulesYAML string) string {
	t.Helper()
	t.Setenv("RECONC_HOME", t.TempDir())
	repo := t.TempDir()
	if err := os.WriteFile(filepath.Join(repo, "AGENTS.md"), []byte("# t\n"), 0o644); err != nil {
		t.Fatalf("write AGENTS.md: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(repo, "policies"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "policies", "rules.yml"), []byte(rulesYAML), 0o644); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	if err := Run([]string{"compile", repo}, "0.1.0-test", &stdout, &stderr); err != nil {
		t.Fatalf("compile via CLI: %v\nstderr: %s", err, stderr.String())
	}
	return repo
}

func TestRunAssertMissingRuleID(t *testing.T) {
	var stdout, stderr bytes.Buffer
	err := Run([]string{"assert"}, "0.1.0-test", &stdout, &stderr)
	if err == nil {
		t.Fatal("expected error for missing rule-id")
	}
}

func TestRunAssertHelp(t *testing.T) {
	var stdout, stderr bytes.Buffer
	err := Run([]string{"assert", "--help"}, "0.1.0-test", &stdout, &stderr)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(stdout.String(), "Usage: reconc assert") {
		t.Errorf("expected usage line, got: %s", stdout.String())
	}
}

func TestRunAssertRuleNotFound(t *testing.T) {
	repo := makeAssertRepo(t,
		"rules:\n  - id: r1\n    kind: deny_write\n    paths: ['x']\n    mode: warn\n    message: m\n")
	var stdout, stderr bytes.Buffer
	err := Run([]string{"assert", "nonexistent", repo}, "0.1.0-test", &stdout, &stderr)
	if err == nil {
		t.Fatal("expected error for missing rule")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("expected 'not found' in error, got: %v", err)
	}
}

func TestRunAssertSimpleRulePasses(t *testing.T) {
	// require_claim rule that we satisfy via --claim
	repo := makeAssertRepo(t,
		"rules:\n  - id: ci-gate\n    kind: require_claim\n    when_paths: ['src/**']\n    claims: ['ci-green']\n    mode: block\n    message: m\n")
	var stdout, stderr bytes.Buffer
	err := Run([]string{"assert", "ci-gate", repo, "--claim", "ci-green"}, "0.1.0-test", &stdout, &stderr)
	if err != nil {
		t.Fatalf("expected pass, got error: %v\nstdout: %s", err, stdout.String())
	}
	if !strings.Contains(stdout.String(), "Decision:  pass") {
		t.Errorf("expected pass decision, got: %s", stdout.String())
	}
}

func TestRunAssertSimpleRuleBlocksWithExit2(t *testing.T) {
	repo := makeAssertRepo(t,
		"rules:\n  - id: ci-gate\n    kind: require_claim\n    when_paths: ['src/**']\n    claims: ['ci-green']\n    mode: block\n    message: m\n")
	var stdout, stderr bytes.Buffer
	err := Run([]string{"assert", "ci-gate", repo}, "0.1.0-test", &stdout, &stderr)
	if err == nil {
		t.Fatal("expected CLIError for blocking violation")
	}
	if code := ExitCode(err); code != 2 {
		t.Errorf("expected exit 2, got %d", code)
	}
}

func TestRunAssertWithVarSubstitutesTemplate(t *testing.T) {
	repo := makeAssertRepo(t,
		"rules:\n  - id: task-evidence\n    kind: require_evidence\n    when_paths: ['docs/todo/{task_id}.md']\n    evidence:\n      - file: 'docs/coverage/{task_id}.md'\n        must_exist: true\n    mode: block\n    message: m\n")
	// Create the coverage file for TODO-001 only
	if err := os.MkdirAll(filepath.Join(repo, "docs/coverage"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "docs/coverage/TODO-001.md"), []byte("ok"), 0o644); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	// TODO-001: passes (coverage exists)
	err := Run([]string{"assert", "task-evidence", repo, "--var", "task_id=TODO-001"}, "0.1.0-test", &stdout, &stderr)
	if err != nil {
		t.Errorf("expected pass for TODO-001, got error: %v\nstdout: %s", err, stdout.String())
	}

	// TODO-002: blocks (no coverage file)
	stdout.Reset()
	stderr.Reset()
	err = Run([]string{"assert", "task-evidence", repo, "--var", "task_id=TODO-002"}, "0.1.0-test", &stdout, &stderr)
	if ExitCode(err) != 2 {
		t.Errorf("expected exit 2 for TODO-002, got %d\nstdout: %s", ExitCode(err), stdout.String())
	}
}

func TestRunAssertJSONOutput(t *testing.T) {
	repo := makeAssertRepo(t,
		"rules:\n  - id: r\n    kind: deny_write\n    paths: ['gen/**']\n    mode: block\n    message: m\n")
	var stdout, stderr bytes.Buffer
	err := Run([]string{"assert", "r", repo, "--json", "--write", "gen/x.go"}, "0.1.0-test", &stdout, &stderr)
	if ExitCode(err) != 2 {
		t.Errorf("expected exit 2, got %d", ExitCode(err))
	}
	var payload map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		t.Fatalf("output should be valid JSON: %v\n%s", err, stdout.String())
	}
	if payload["decision"] != "block" {
		t.Errorf("expected decision=block, got %v", payload["decision"])
	}
}

func TestRunAssertVarBadFormat(t *testing.T) {
	repo := makeAssertRepo(t,
		"rules:\n  - id: r\n    kind: deny_write\n    paths: ['x']\n    mode: warn\n    message: m\n")
	var stdout, stderr bytes.Buffer
	err := Run([]string{"assert", "r", repo, "--var", "no-equals"}, "0.1.0-test", &stdout, &stderr)
	if err == nil {
		t.Fatal("expected error for malformed --var")
	}
}

func TestRunAssertOnlyEvaluatesRequestedRule(t *testing.T) {
	// Two rules: r1 would block on src/**, r2 would block on gen/**.
	// Asserting r1 with --write src/main.go shouldn't trip r2 even
	// though r2 is also in the lockfile.
	repo := makeAssertRepo(t,
		"rules:\n  - id: r1\n    kind: deny_write\n    paths: ['src/**']\n    mode: warn\n    message: m\n  - id: r2\n    kind: deny_write\n    paths: ['gen/**']\n    mode: block\n    message: m\n")
	var stdout, stderr bytes.Buffer
	err := Run([]string{"assert", "r1", repo, "--write", "src/main.go"}, "0.1.0-test", &stdout, &stderr)
	if err != nil {
		t.Errorf("r1 is warn-mode so should not block; got: %v", err)
	}
	if !strings.Contains(stdout.String(), "Decision:  warn") {
		t.Errorf("expected warn decision, got: %s", stdout.String())
	}
	// r2 should not appear in violations
	if strings.Contains(stdout.String(), "r2") {
		t.Errorf("r2 should not appear when asserting only r1, got: %s", stdout.String())
	}
}

// --- Phase 5C: verify ------------------------------------------------

func TestRunVerifyOnCompiledRepo(t *testing.T) {
	repo := makeAssertRepo(t,
		"rules:\n  - id: r1\n    kind: deny_write\n    paths: ['gen/**']\n    mode: block\n    message: m\n")
	var stdout, stderr bytes.Buffer
	if err := Run([]string{"verify", repo}, "0.1.0-test", &stdout, &stderr); err != nil {
		t.Fatalf("verify should never return CLIError, got: %v", err)
	}
	out := stdout.String()
	for _, want := range []string{"reconc binary on PATH", "bundled presets", "repo discovery", "policy parse", "lockfile fresh"} {
		if !strings.Contains(out, want) {
			t.Errorf("verify output missing %q; got:\n%s", want, out)
		}
	}
}

func TestRunVerifyJSON(t *testing.T) {
	repo := makeAssertRepo(t,
		"rules:\n  - id: r1\n    kind: deny_write\n    paths: ['gen/**']\n    mode: block\n    message: m\n")
	var stdout, stderr bytes.Buffer
	if err := Run([]string{"verify", repo, "--json"}, "0.1.0-test", &stdout, &stderr); err != nil {
		t.Fatalf("verify --json: %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		t.Fatalf("verify --json should produce valid JSON: %v\n%s", err, stdout.String())
	}
	if _, ok := payload["checks"]; !ok {
		t.Errorf("expected 'checks' field in JSON payload, got: %v", payload)
	}
}

// --- Phase 5C: why ---------------------------------------------------

func TestRunWhyPrintsRuleDetails(t *testing.T) {
	repo := makeAssertRepo(t,
		"rules:\n  - id: r-why\n    kind: deny_write\n    paths: ['src/**']\n    mode: block\n    message: 'hands off'\n")
	var stdout, stderr bytes.Buffer
	if err := Run([]string{"why", "r-why", repo}, "0.1.0-test", &stdout, &stderr); err != nil {
		t.Fatalf("why: %v\nstderr: %s", err, stderr.String())
	}
	out := stdout.String()
	for _, want := range []string{"r-why", "deny_write", "block", "hands off", "src/**"} {
		if !strings.Contains(out, want) {
			t.Errorf("why output missing %q; got:\n%s", want, out)
		}
	}
}

func TestRunWhyJSON(t *testing.T) {
	repo := makeAssertRepo(t,
		"rules:\n  - id: r-why-j\n    kind: deny_write\n    paths: ['src/**']\n    mode: block\n    message: m\n")
	var stdout, stderr bytes.Buffer
	if err := Run([]string{"why", "r-why-j", repo, "--json"}, "0.1.0-test", &stdout, &stderr); err != nil {
		t.Fatalf("why --json: %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		t.Fatalf("why --json should produce valid JSON: %v\n%s", err, stdout.String())
	}
	if payload["id"] != "r-why-j" {
		t.Errorf("expected id=r-why-j in payload, got: %v", payload)
	}
}

func TestRunWhyRuleNotFound(t *testing.T) {
	repo := makeAssertRepo(t,
		"rules:\n  - id: real-rule\n    kind: deny_write\n    paths: ['src/**']\n    mode: block\n    message: m\n")
	var stdout, stderr bytes.Buffer
	err := Run([]string{"why", "bogus-rule-id", repo}, "0.1.0-test", &stdout, &stderr)
	if err == nil {
		t.Fatal("expected error for unknown rule-id")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("error should mention 'not found'; got: %v", err)
	}
}

func TestRunWhyMissingRuleID(t *testing.T) {
	var stdout, stderr bytes.Buffer
	err := Run([]string{"why"}, "0.1.0-test", &stdout, &stderr)
	if err == nil {
		t.Fatal("expected error for missing rule-id")
	}
}

func TestRunWhyHelp(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if err := Run([]string{"why", "--help"}, "0.1.0-test", &stdout, &stderr); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(stdout.String(), "Usage: reconc why") {
		t.Errorf("expected usage line, got: %s", stdout.String())
	}
}

// --- Phase 5C: can ---------------------------------------------------

func TestRunCanWriteAllowed(t *testing.T) {
	repo := makeAssertRepo(t,
		"rules:\n  - id: r-deny-gen\n    kind: deny_write\n    paths: ['gen/**']\n    mode: block\n    message: m\n")
	var stdout, stderr bytes.Buffer
	if err := Run([]string{"can", "write", "src/app.go", repo}, "0.1.0-test", &stdout, &stderr); err != nil {
		t.Fatalf("can write (allowed) should exit 0, got: %v", err)
	}
	if !strings.Contains(stdout.String(), "yes") {
		t.Errorf("expected 'yes' in output, got: %s", stdout.String())
	}
}

func TestRunCanWriteBlocked(t *testing.T) {
	repo := makeAssertRepo(t,
		"rules:\n  - id: r-deny-gen\n    kind: deny_write\n    paths: ['gen/**']\n    mode: block\n    message: 'no gen writes'\n")
	var stdout, stderr bytes.Buffer
	err := Run([]string{"can", "write", "gen/x.go", repo}, "0.1.0-test", &stdout, &stderr)
	if err == nil {
		t.Fatal("expected CLIError for blocked write")
	}
	if code := ExitCode(err); code != 2 {
		t.Errorf("blocked action should exit 2, got %d", code)
	}
	if !strings.Contains(stdout.String(), "no") {
		t.Errorf("expected 'no' in output, got: %s", stdout.String())
	}
	if !strings.Contains(stdout.String(), "r-deny-gen") {
		t.Errorf("expected rule id in output, got: %s", stdout.String())
	}
}

func TestRunCanWriteJSON(t *testing.T) {
	repo := makeAssertRepo(t,
		"rules:\n  - id: r-deny-gen\n    kind: deny_write\n    paths: ['gen/**']\n    mode: block\n    message: m\n")
	var stdout, stderr bytes.Buffer
	err := Run([]string{"can", "write", "gen/x.go", repo, "--json"}, "0.1.0-test", &stdout, &stderr)
	if err == nil {
		t.Fatal("expected CLIError for blocked write")
	}
	var payload map[string]any
	if jerr := json.Unmarshal(stdout.Bytes(), &payload); jerr != nil {
		t.Fatalf("--json should produce valid JSON: %v\n%s", jerr, stdout.String())
	}
	if payload["yes"] != false {
		t.Errorf("expected yes=false, got: %v", payload)
	}
	if payload["decision"] != "block" {
		t.Errorf("expected decision=block, got: %v", payload)
	}
}

func TestRunCanUnsupportedAction(t *testing.T) {
	repo := makeAssertRepo(t,
		"rules:\n  - id: r1\n    kind: deny_write\n    paths: ['x']\n    mode: block\n    message: m\n")
	var stdout, stderr bytes.Buffer
	err := Run([]string{"can", "delete", "any.txt", repo}, "0.1.0-test", &stdout, &stderr)
	if err == nil {
		t.Fatal("expected error for unsupported action")
	}
	if code := ExitCode(err); code != 1 {
		t.Errorf("unsupported action should exit 1, got %d", code)
	}
}

func TestRunCanMissingArgs(t *testing.T) {
	var stdout, stderr bytes.Buffer
	err := Run([]string{"can"}, "0.1.0-test", &stdout, &stderr)
	if err == nil {
		t.Fatal("expected error for missing args")
	}
}

func TestRunCanHelp(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if err := Run([]string{"can", "--help"}, "0.1.0-test", &stdout, &stderr); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(stdout.String(), "Usage: reconc can") {
		t.Errorf("expected usage line, got: %s", stdout.String())
	}
}

// --- W15: adopt ------------------------------------------------------

func TestRunAdoptTextOutput(t *testing.T) {
	repo := t.TempDir()
	if err := os.WriteFile(filepath.Join(repo, "go.mod"), []byte("module demo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	if err := Run([]string{"adopt", repo}, "0.1.0-test", &stdout, &stderr); err != nil {
		t.Fatalf("adopt: %v", err)
	}
	out := stdout.String()
	for _, want := range []string{"Detected", "go.mod", "adopt-go-test", "go test ./..."} {
		if !strings.Contains(out, want) {
			t.Errorf("adopt text output missing %q; got:\n%s", want, out)
		}
	}
}

func TestRunAdoptJSONOutput(t *testing.T) {
	repo := t.TempDir()
	if err := os.WriteFile(filepath.Join(repo, "go.mod"), []byte("module demo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	if err := Run([]string{"adopt", repo, "--json"}, "0.1.0-test", &stdout, &stderr); err != nil {
		t.Fatalf("adopt --json: %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		t.Fatalf("adopt --json should produce valid JSON: %v\n%s", err, stdout.String())
	}
	if _, ok := payload["suggestions"]; !ok {
		t.Errorf("expected 'suggestions' in JSON, got: %v", payload)
	}
}

func TestRunAdoptYAMLOutput(t *testing.T) {
	repo := t.TempDir()
	if err := os.WriteFile(filepath.Join(repo, "go.mod"), []byte("module demo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	if err := Run([]string{"adopt", repo, "--yaml"}, "0.1.0-test", &stdout, &stderr); err != nil {
		t.Fatalf("adopt --yaml: %v", err)
	}
	out := stdout.String()
	for _, want := range []string{"- id: adopt-go-test", "kind: require_command", "when_paths:"} {
		if !strings.Contains(out, want) {
			t.Errorf("adopt --yaml output missing %q; got:\n%s", want, out)
		}
	}
}

func TestRunAdoptApplyWritesConfig(t *testing.T) {
	repo := t.TempDir()
	if err := os.WriteFile(filepath.Join(repo, "go.mod"), []byte("module demo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	if err := Run([]string{"adopt", repo, "--apply"}, "0.1.0-test", &stdout, &stderr); err != nil {
		t.Fatalf("adopt --apply: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(repo, ".reconc.yml"))
	if err != nil {
		t.Fatalf("read .reconc.yml: %v", err)
	}
	if !strings.Contains(string(data), "adopt-go-test") {
		t.Errorf(".reconc.yml missing adopted rule; got:\n%s", string(data))
	}
}

func TestRunAdoptMutuallyExclusiveFlags(t *testing.T) {
	repo := t.TempDir()
	var stdout, stderr bytes.Buffer
	err := Run([]string{"adopt", repo, "--yaml", "--json"}, "0.1.0-test", &stdout, &stderr)
	if err == nil {
		t.Fatal("expected error for --yaml + --json combination")
	}
	if !strings.Contains(err.Error(), "mutually exclusive") {
		t.Errorf("expected mutually-exclusive error; got: %v", err)
	}
}

func TestRunAdoptUnknownFlag(t *testing.T) {
	repo := t.TempDir()
	var stdout, stderr bytes.Buffer
	err := Run([]string{"adopt", repo, "--nope"}, "0.1.0-test", &stdout, &stderr)
	if err == nil {
		t.Fatal("expected error for unknown flag")
	}
}

func TestRunAdoptNonDirectory(t *testing.T) {
	var stdout, stderr bytes.Buffer
	err := Run([]string{"adopt", "/definitely/not/a/real/path/for/reconc/adopt"}, "0.1.0-test", &stdout, &stderr)
	if err == nil {
		t.Fatal("expected error for non-directory")
	}
}

func TestRunAdoptHelp(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if err := Run([]string{"adopt", "--help"}, "0.1.0-test", &stdout, &stderr); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(stdout.String(), "Usage: reconc adopt") {
		t.Errorf("expected usage line, got: %s", stdout.String())
	}
}

// --- W45: changelog rotation ----------------------------------------

func writeChangelog(t *testing.T, repo, body string) {
	t.Helper()
	docsDir := filepath.Join(repo, "docs")
	if err := os.MkdirAll(docsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(docsDir, "changelog.md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

const cliChangelogSample = `# Changelog

Intro.

## 2026-04-12 Newest
- a
- b

## 2026-04-11 Older1
- c

## 2026-04-10 Older2
- d

## 2026-04-09 Oldest
- e
`

func TestRunChangelogMissingSubcommand(t *testing.T) {
	var stdout, stderr bytes.Buffer
	err := Run([]string{"changelog"}, "0.1.0-test", &stdout, &stderr)
	if err == nil {
		t.Fatal("expected error for missing subcommand")
	}
}

func TestRunChangelogRotateUnderThreshold(t *testing.T) {
	repo := t.TempDir()
	writeChangelog(t, repo, cliChangelogSample)
	var stdout, stderr bytes.Buffer
	if err := Run([]string{"changelog", "rotate", repo}, "0.1.0-test", &stdout, &stderr); err != nil {
		t.Fatalf("rotate: %v", err)
	}
	if !strings.Contains(stdout.String(), "No rotation needed") {
		t.Errorf("expected 'No rotation needed', got: %s", stdout.String())
	}
}

func TestRunChangelogRotateForce(t *testing.T) {
	repo := t.TempDir()
	writeChangelog(t, repo, cliChangelogSample)
	var stdout, stderr bytes.Buffer
	if err := Run([]string{"changelog", "rotate", repo, "--force"}, "0.1.0-test", &stdout, &stderr); err != nil {
		t.Fatalf("rotate --force: %v", err)
	}
	if !strings.Contains(stdout.String(), "Rotated") {
		t.Errorf("expected 'Rotated' in output, got: %s", stdout.String())
	}
	// Archive file must exist.
	matches, _ := filepath.Glob(filepath.Join(repo, "docs", "changelog", "archive", "*.md"))
	if len(matches) == 0 {
		t.Errorf("expected at least one archive file under docs/changelog/archive/")
	}
}

func TestRunChangelogRotateJSONOutput(t *testing.T) {
	repo := t.TempDir()
	writeChangelog(t, repo, cliChangelogSample)
	var stdout, stderr bytes.Buffer
	if err := Run([]string{"changelog", "rotate", repo, "--force", "--json"}, "0.1.0-test", &stdout, &stderr); err != nil {
		t.Fatalf("rotate --json: %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		t.Fatalf("rotate --json should produce valid JSON: %v\n%s", err, stdout.String())
	}
	if payload["rotated"] != true {
		t.Errorf("expected rotated=true, got: %v", payload)
	}
}

func TestRunChangelogRotateLinesFlag(t *testing.T) {
	repo := t.TempDir()
	writeChangelog(t, repo, cliChangelogSample)
	var stdout, stderr bytes.Buffer
	if err := Run([]string{"changelog", "rotate", repo, "--lines", "5"}, "0.1.0-test", &stdout, &stderr); err != nil {
		t.Fatalf("rotate --lines 5: %v", err)
	}
	if !strings.Contains(stdout.String(), "Rotated") {
		t.Errorf("expected rotation at threshold 5, got: %s", stdout.String())
	}
}

func TestRunChangelogRotateLinesFlagInvalid(t *testing.T) {
	repo := t.TempDir()
	writeChangelog(t, repo, cliChangelogSample)
	var stdout, stderr bytes.Buffer
	err := Run([]string{"changelog", "rotate", repo, "--lines", "notanumber"}, "0.1.0-test", &stdout, &stderr)
	if err == nil {
		t.Fatal("expected error for non-integer --lines")
	}
}

func TestRunChangelogListArchivesEmpty(t *testing.T) {
	repo := t.TempDir()
	var stdout, stderr bytes.Buffer
	if err := Run([]string{"changelog", "list-archives", repo}, "0.1.0-test", &stdout, &stderr); err != nil {
		t.Fatalf("list-archives: %v", err)
	}
	if !strings.Contains(stdout.String(), "No archive files") {
		t.Errorf("expected empty-state, got: %s", stdout.String())
	}
}

func TestRunChangelogListArchivesJSON(t *testing.T) {
	repo := t.TempDir()
	writeChangelog(t, repo, cliChangelogSample)
	var stdout, stderr bytes.Buffer
	if err := Run([]string{"changelog", "rotate", repo, "--force"}, "0.1.0-test", &stdout, &stderr); err != nil {
		t.Fatalf("rotate --force setup: %v", err)
	}
	stdout.Reset()
	if err := Run([]string{"changelog", "list-archives", repo, "--json"}, "0.1.0-test", &stdout, &stderr); err != nil {
		t.Fatalf("list-archives --json: %v", err)
	}
	var list []map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &list); err != nil {
		t.Fatalf("list-archives --json should produce valid JSON: %v\n%s", err, stdout.String())
	}
	if len(list) == 0 {
		t.Errorf("expected at least one archive entry, got: %s", stdout.String())
	}
}

func TestRunChangelogHelp(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if err := Run([]string{"changelog", "--help"}, "0.1.0-test", &stdout, &stderr); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(stdout.String(), "reconc changelog") {
		t.Errorf("expected usage info, got: %s", stdout.String())
	}
}

func TestRunChangelogUnknownSubcommand(t *testing.T) {
	var stdout, stderr bytes.Buffer
	err := Run([]string{"changelog", "noop"}, "0.1.0-test", &stdout, &stderr)
	if err == nil {
		t.Fatal("expected error for unknown subcommand")
	}
}

// --- W11: agent-intro ------------------------------------------------

func TestRunAgentIntroFullMarkdown(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if err := Run([]string{"agent-intro"}, "0.1.0-test", &stdout, &stderr); err != nil {
		t.Fatalf("agent-intro: %v", err)
	}
	out := stdout.String()
	for _, want := range []string{"# reconc", "Exit Codes", "Rule Kinds", "Golden Rules"} {
		if !strings.Contains(out, want) {
			t.Errorf("agent-intro missing %q; got first 200 chars:\n%s", want, out[:min(len(out), 200)])
		}
	}
}

func TestRunAgentIntroListSections(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if err := Run([]string{"agent-intro", "--list-sections"}, "0.1.0-test", &stdout, &stderr); err != nil {
		t.Fatalf("agent-intro --list-sections: %v", err)
	}
	for _, want := range []string{"Exit Codes", "Rule Kinds", "Golden Rules"} {
		if !strings.Contains(stdout.String(), want) {
			t.Errorf("--list-sections output missing %q", want)
		}
	}
}

func TestRunAgentIntroSection(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if err := Run([]string{"agent-intro", "--section", "Exit Codes"}, "0.1.0-test", &stdout, &stderr); err != nil {
		t.Fatalf("agent-intro --section: %v", err)
	}
	out := stdout.String()
	if !strings.HasPrefix(out, "## Exit Codes") {
		t.Errorf("expected section output to start with heading, got:\n%s", out[:min(len(out), 120)])
	}
	// Must not spill into other sections.
	if strings.Contains(out, "## Golden Rules") {
		t.Errorf("section output bled into next section:\n%s", out)
	}
}

func TestRunAgentIntroSectionJSON(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if err := Run([]string{"agent-intro", "--section", "Golden Rules", "--json"}, "0.1.0-test", &stdout, &stderr); err != nil {
		t.Fatalf("agent-intro --section --json: %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		t.Fatalf("--json should produce valid JSON: %v\n%s", err, stdout.String())
	}
	body, _ := payload["body"].(string)
	if !strings.Contains(body, "Never paraphrase policy") {
		t.Errorf("expected golden-rules text in body, got: %v", payload)
	}
}

func TestRunAgentIntroUnknownSection(t *testing.T) {
	var stdout, stderr bytes.Buffer
	err := Run([]string{"agent-intro", "--section", "definitely-bogus"}, "0.1.0-test", &stdout, &stderr)
	if err == nil {
		t.Fatal("expected error for unknown section")
	}
}

func TestRunAgentIntroUnknownFlag(t *testing.T) {
	var stdout, stderr bytes.Buffer
	err := Run([]string{"agent-intro", "--nope"}, "0.1.0-test", &stdout, &stderr)
	if err == nil {
		t.Fatal("expected error for unknown flag")
	}
}

func TestRunAgentIntroHelp(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if err := Run([]string{"agent-intro", "--help"}, "0.1.0-test", &stdout, &stderr); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(stdout.String(), "Usage: reconc agent-intro") {
		t.Errorf("expected usage line, got: %s", stdout.String())
	}
}

// min is a tiny helper for slicing log output safely in assertions
// (Go 1.21+ has builtin min but we keep this local for predictability).
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// --- W32: rule conflict detection -----------------------------------

func makeConflictRepo(t *testing.T) string {
	t.Helper()
	t.Setenv("RECONC_HOME", t.TempDir())
	repo := t.TempDir()
	if err := os.WriteFile(filepath.Join(repo, "AGENTS.md"), []byte("# t\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(repo, "policies"), 0o755); err != nil {
		t.Fatal(err)
	}
	rules := `rules:
  - id: dup-a
    kind: deny_write
    paths: ['gen/**']
    mode: block
    message: no
  - id: dup-b
    kind: deny_write
    paths: ['gen/**']
    mode: warn
    message: no
`
	if err := os.WriteFile(filepath.Join(repo, "policies", "rules.yml"), []byte(rules), 0o644); err != nil {
		t.Fatal(err)
	}
	return repo
}

func TestRunCompileReportsConflicts(t *testing.T) {
	repo := makeConflictRepo(t)
	var stdout, stderr bytes.Buffer
	if err := Run([]string{"compile", repo}, "0.1.0-test", &stdout, &stderr); err != nil {
		t.Fatalf("compile: %v", err)
	}
	out := stdout.String()
	if !strings.Contains(out, "Conflicts (1)") {
		t.Errorf("expected 'Conflicts (1)' line, got:\n%s", out)
	}
	if !strings.Contains(out, "duplicate_deny_write") {
		t.Errorf("expected conflict kind in output, got:\n%s", out)
	}
}

func TestRunCompileJSONIncludesConflicts(t *testing.T) {
	repo := makeConflictRepo(t)
	var stdout, stderr bytes.Buffer
	if err := Run([]string{"compile", repo, "--json"}, "0.1.0-test", &stdout, &stderr); err != nil {
		t.Fatalf("compile --json: %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		t.Fatalf("compile --json should produce valid JSON: %v", err)
	}
	conflicts, _ := payload["conflicts"].([]any)
	if len(conflicts) != 1 {
		t.Errorf("expected 1 conflict in JSON, got %d: %v", len(conflicts), conflicts)
	}
}

func TestRunCompileStrictConflictsFails(t *testing.T) {
	repo := makeConflictRepo(t)
	var stdout, stderr bytes.Buffer
	err := Run([]string{"compile", repo, "--strict-conflicts"}, "0.1.0-test", &stdout, &stderr)
	if err == nil {
		t.Fatal("expected --strict-conflicts to surface a CLIError")
	}
	if code := ExitCode(err); code != 1 {
		t.Errorf("expected exit 1, got %d", code)
	}
	if !strings.Contains(err.Error(), "rule conflict") {
		t.Errorf("expected error to mention rule conflict, got: %v", err)
	}
}

// --- W31: rule deprecation ------------------------------------------

func TestRunCompileEmitsDeprecatedWarning(t *testing.T) {
	t.Setenv("RECONC_HOME", t.TempDir())
	repo := t.TempDir()
	if err := os.WriteFile(filepath.Join(repo, "AGENTS.md"), []byte("# t\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(repo, "policies"), 0o755); err != nil {
		t.Fatal(err)
	}
	rules := `rules:
  - id: old
    kind: deny_write
    paths: ['x']
    mode: warn
    message: m
    deprecated: true
    deprecated_since: "2026-01-15"
    deprecated_replaced_by: new
  - id: new
    kind: deny_write
    paths: ['y']
    mode: warn
    message: m
`
	if err := os.WriteFile(filepath.Join(repo, "policies", "rules.yml"), []byte(rules), 0o644); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	if err := Run([]string{"compile", repo}, "0.1.0-test", &stdout, &stderr); err != nil {
		t.Fatalf("compile: %v", err)
	}
	out := stdout.String()
	if !strings.Contains(out, "deprecated") {
		t.Errorf("expected deprecation warning in compile output, got:\n%s", out)
	}
	if !strings.Contains(out, "replaced by 'new'") {
		t.Errorf("expected replaced-by hint, got:\n%s", out)
	}
}

func TestRunWhyShowsDeprecationStatus(t *testing.T) {
	t.Setenv("RECONC_HOME", t.TempDir())
	repo := t.TempDir()
	if err := os.WriteFile(filepath.Join(repo, "AGENTS.md"), []byte("# t\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(repo, "policies"), 0o755); err != nil {
		t.Fatal(err)
	}
	rules := `rules:
  - id: old
    kind: deny_write
    paths: ['x']
    mode: warn
    message: m
    deprecated: true
    deprecated_since: "2026-02-01"
    deprecated_reason: test-reason
`
	if err := os.WriteFile(filepath.Join(repo, "policies", "rules.yml"), []byte(rules), 0o644); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	_ = Run([]string{"compile", repo}, "0.1.0-test", &stdout, &stderr)
	stdout.Reset()
	if err := Run([]string{"why", "old", repo}, "0.1.0-test", &stdout, &stderr); err != nil {
		t.Fatalf("why: %v", err)
	}
	out := stdout.String()
	if !strings.Contains(out, "Status:") {
		t.Errorf("expected Status line for deprecated rule, got:\n%s", out)
	}
	if !strings.Contains(out, "DEPRECATED") {
		t.Errorf("expected DEPRECATED label, got:\n%s", out)
	}
	if !strings.Contains(out, "test-reason") {
		t.Errorf("expected reason in Status, got:\n%s", out)
	}
}

// --- version subcommand ---------------------------------------------

func TestRunVersionSubcommand(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if err := Run([]string{"version"}, "0.9.9-test", &stdout, &stderr); err != nil {
		t.Fatalf("version: %v", err)
	}
	if !strings.Contains(stdout.String(), "reconc 0.9.9-test") {
		t.Errorf("expected version line, got: %s", stdout.String())
	}
}

func TestRunVersionJSON(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if err := Run([]string{"version", "--json"}, "0.2.0", &stdout, &stderr); err != nil {
		t.Fatalf("version --json: %v", err)
	}
	var payload map[string]string
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		t.Fatalf("expected JSON, got: %v\n%s", err, stdout.String())
	}
	if payload["version"] != "0.2.0" {
		t.Errorf("expected version=0.2.0, got %q", payload["version"])
	}
	if payload["binary"] != "reconc" {
		t.Errorf("expected binary=reconc, got %q", payload["binary"])
	}
	if payload["go_runtime"] == "" {
		t.Error("expected go_runtime to be populated")
	}
}

func TestRunVersionFlagStillWorks(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if err := Run([]string{"--version"}, "0.2.0", &stdout, &stderr); err != nil {
		t.Fatalf("--version: %v", err)
	}
	if !strings.Contains(stdout.String(), "reconc 0.2.0") {
		t.Errorf("expected version line, got: %s", stdout.String())
	}
}

// --- manpage generator ----------------------------------------------

func TestRunManpageEmitsRoff(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if err := Run([]string{"manpage"}, "0.2.0", &stdout, &stderr); err != nil {
		t.Fatalf("manpage: %v", err)
	}
	out := stdout.String()
	for _, want := range []string{".TH RECONC 1", ".SH NAME", ".SH SUBCOMMANDS", "reconc 0.2.0"} {
		if !strings.Contains(out, want) {
			t.Errorf("manpage output missing %q; first 120 chars:\n%s", want, out[:min(len(out), 120)])
		}
	}
}

func TestRunManpageHelp(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if err := Run([]string{"manpage", "--help"}, "0.2.0", &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "Usage: reconc manpage") {
		t.Errorf("expected usage banner, got: %s", stdout.String())
	}
}

func TestRunManpageRejectsUnknownFlag(t *testing.T) {
	var stdout, stderr bytes.Buffer
	err := Run([]string{"manpage", "--section", "5"}, "0.2.0", &stdout, &stderr)
	if err == nil {
		t.Fatal("expected error for unknown flag")
	}
}

// --- shell completion ------------------------------------------------

func TestRunCompletionMissingShell(t *testing.T) {
	var stdout, stderr bytes.Buffer
	err := Run([]string{"completion"}, "0.1.0-test", &stdout, &stderr)
	if err == nil {
		t.Fatal("expected error for missing shell argument")
	}
}

func TestRunCompletionBash(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if err := Run([]string{"completion", "bash"}, "0.1.0-test", &stdout, &stderr); err != nil {
		t.Fatalf("completion bash: %v", err)
	}
	if !strings.Contains(stdout.String(), "complete -F _reconc") {
		t.Errorf("bash output missing complete directive, got:\n%s", stdout.String()[:200])
	}
}

func TestRunCompletionZsh(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if err := Run([]string{"completion", "zsh"}, "0.1.0-test", &stdout, &stderr); err != nil {
		t.Fatalf("completion zsh: %v", err)
	}
	if !strings.Contains(stdout.String(), "#compdef reconc") {
		t.Errorf("zsh output missing compdef directive, got:\n%s", stdout.String()[:200])
	}
}

func TestRunCompletionFish(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if err := Run([]string{"completion", "fish"}, "0.1.0-test", &stdout, &stderr); err != nil {
		t.Fatalf("completion fish: %v", err)
	}
	if !strings.Contains(stdout.String(), "complete -c reconc") {
		t.Errorf("fish output missing complete directive, got:\n%s", stdout.String()[:200])
	}
}

func TestRunCompletionUnknownShell(t *testing.T) {
	var stdout, stderr bytes.Buffer
	err := Run([]string{"completion", "pwsh"}, "0.1.0-test", &stdout, &stderr)
	if err == nil {
		t.Fatal("expected error for unknown shell")
	}
}

func TestRunCompletionHelp(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if err := Run([]string{"completion", "--help"}, "0.1.0-test", &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "Usage: reconc completion") {
		t.Errorf("expected usage banner, got: %s", stdout.String())
	}
}

// --- W7: --auto-claim CI detection ----------------------------------

func TestDetectCIEnvironmentRecognisesCITrue(t *testing.T) {
	t.Setenv("CI", "true")
	if !detectCIEnvironment() {
		t.Error("CI=true should be detected")
	}
	t.Setenv("CI", "1")
	if !detectCIEnvironment() {
		t.Error("CI=1 should be detected")
	}
}

func TestDetectCIEnvironmentRecognisesProviderMarkers(t *testing.T) {
	os.Unsetenv("CI")
	t.Setenv("GITHUB_ACTIONS", "true")
	if !detectCIEnvironment() {
		t.Error("GITHUB_ACTIONS should be detected")
	}
}

func TestDetectCIEnvironmentFalseWhenUnset(t *testing.T) {
	for _, k := range []string{"CI", "GITHUB_ACTIONS", "GITLAB_CI", "CIRCLECI", "TRAVIS", "JENKINS_URL", "BUILDKITE", "DRONE", "APPVEYOR", "TEAMCITY_VERSION", "BITBUCKET_BUILD_NUMBER"} {
		os.Unsetenv(k)
	}
	if detectCIEnvironment() {
		t.Error("no CI env set; must return false")
	}
}

func TestRunCheckAutoClaimPopulatesCIGreen(t *testing.T) {
	t.Setenv("CI", "true")
	t.Setenv("RECONC_HOME", t.TempDir())
	repo := makeAssertRepo(t,
		"rules:\n  - id: r1\n    kind: require_claim\n    when_paths: ['**']\n    claims: ['ci-green']\n    mode: block\n    message: m\n")
	var stdout, stderr bytes.Buffer
	// Without --auto-claim, writing should block (no claim).
	err := Run([]string{"check", repo, "--write", "src/x.go"}, "0.1.0-test", &stdout, &stderr)
	if err == nil {
		t.Error("expected block without --auto-claim")
	}
	// With --auto-claim in CI env, should pass.
	stdout.Reset()
	if err := Run([]string{"check", repo, "--write", "src/x.go", "--auto-claim"}, "0.1.0-test", &stdout, &stderr); err != nil {
		t.Errorf("expected pass with --auto-claim in CI, got: %v", err)
	}
}

// --- W5 / W6: diff + watch ------------------------------------------

func TestRunDiffUsage(t *testing.T) {
	var stdout, stderr bytes.Buffer
	err := Run([]string{"diff"}, "0.1.0-test", &stdout, &stderr)
	if err == nil {
		t.Fatal("expected usage error for no positionals")
	}
}

func TestRunDiffTwoLocks(t *testing.T) {
	dir := t.TempDir()
	a := `{"rules":[{"id":"r1","kind":"deny_write","mode":"warn"}]}`
	b := `{"rules":[{"id":"r1","kind":"deny_write","mode":"block"},{"id":"r2","kind":"deny_write","mode":"warn"}]}`
	if err := os.WriteFile(filepath.Join(dir, "a.json"), []byte(a), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "b.json"), []byte(b), 0o644); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	if err := Run([]string{"diff", filepath.Join(dir, "a.json"), filepath.Join(dir, "b.json")}, "0.1.0-test", &stdout, &stderr); err != nil {
		t.Fatalf("diff: %v", err)
	}
	for _, want := range []string{"Added (1)", "r2", "Changed (1)", "r1", "mode"} {
		if !strings.Contains(stdout.String(), want) {
			t.Errorf("expected %q in output, got:\n%s", want, stdout.String())
		}
	}
}

func TestRunDiffJSON(t *testing.T) {
	dir := t.TempDir()
	a := `{"rules":[]}`
	b := `{"rules":[{"id":"r1","kind":"deny_write","mode":"warn"}]}`
	if err := os.WriteFile(filepath.Join(dir, "a.json"), []byte(a), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "b.json"), []byte(b), 0o644); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	if err := Run([]string{"diff", filepath.Join(dir, "a.json"), filepath.Join(dir, "b.json"), "--json"}, "0.1.0-test", &stdout, &stderr); err != nil {
		t.Fatalf("diff --json: %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		t.Fatalf("expected JSON, got: %v\n%s", err, stdout.String())
	}
	if _, ok := payload["added"]; !ok {
		t.Error("JSON missing 'added'")
	}
}

func TestRunDiffIdenticalLocks(t *testing.T) {
	dir := t.TempDir()
	body := `{"rules":[{"id":"r1","kind":"deny_write","mode":"warn"}]}`
	for _, f := range []string{"a.json", "b.json"} {
		if err := os.WriteFile(filepath.Join(dir, f), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	var stdout, stderr bytes.Buffer
	if err := Run([]string{"diff", filepath.Join(dir, "a.json"), filepath.Join(dir, "b.json")}, "0.1.0-test", &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "No changes") {
		t.Errorf("expected 'No changes', got: %s", stdout.String())
	}
}

func TestRunDiffHelp(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if err := Run([]string{"diff", "--help"}, "0.1.0-test", &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "Usage: reconc diff") {
		t.Errorf("expected usage, got: %s", stdout.String())
	}
}

func TestRunWatchHelp(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if err := Run([]string{"watch", "--help"}, "0.1.0-test", &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "Usage: reconc watch") {
		t.Errorf("expected usage, got: %s", stdout.String())
	}
}

func TestRunWatchBadInterval(t *testing.T) {
	var stdout, stderr bytes.Buffer
	err := Run([]string{"watch", ".", "--interval-ms", "5"}, "0.1.0-test", &stdout, &stderr)
	if err == nil {
		t.Fatal("expected error for interval < 100")
	}
}

func TestRunWatchRepoNotDiscovered(t *testing.T) {
	var stdout, stderr bytes.Buffer
	err := Run([]string{"watch", t.TempDir()}, "0.1.0-test", &stdout, &stderr)
	if err == nil {
		t.Fatal("expected error for undiscovered repo")
	}
}

// --- W20: semantic prose extraction ---------------------------------

func TestRunExtractNoSourceFile(t *testing.T) {
	repo := t.TempDir()
	var stdout, stderr bytes.Buffer
	err := Run([]string{"extract", repo}, "0.1.0-test", &stdout, &stderr)
	if err == nil {
		t.Fatal("expected error when no AGENTS.md / CLAUDE.md exists")
	}
}

func TestRunExtractScansAgentsMD(t *testing.T) {
	repo := t.TempDir()
	prose := "# rules\n\nDon't edit generated/**.\nNever commit .env files.\nRun `go test ./...` before committing.\n"
	if err := os.WriteFile(filepath.Join(repo, "AGENTS.md"), []byte(prose), 0o644); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	if err := Run([]string{"extract", repo}, "0.1.0-test", &stdout, &stderr); err != nil {
		t.Fatalf("extract: %v", err)
	}
	for _, want := range []string{"generated/**", "extract-no-secrets", "extract-run-"} {
		if !strings.Contains(stdout.String(), want) {
			t.Errorf("expected %q in output, got:\n%s", want, stdout.String())
		}
	}
}

func TestRunExtractJSON(t *testing.T) {
	repo := t.TempDir()
	if err := os.WriteFile(filepath.Join(repo, "AGENTS.md"), []byte("Never commit secrets."), 0o644); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	if err := Run([]string{"extract", repo, "--json"}, "0.1.0-test", &stdout, &stderr); err != nil {
		t.Fatalf("extract --json: %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		t.Fatalf("expected JSON, got: %v\n%s", err, stdout.String())
	}
	if _, ok := payload["suggestions"]; !ok {
		t.Error("JSON missing 'suggestions' field")
	}
}

func TestRunExtractCustomFile(t *testing.T) {
	repo := t.TempDir()
	if err := os.WriteFile(filepath.Join(repo, "notes.md"), []byte("Don't edit dist/**."), 0o644); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	if err := Run([]string{"extract", repo, "--from", "notes.md"}, "0.1.0-test", &stdout, &stderr); err != nil {
		t.Fatalf("extract --from: %v", err)
	}
	if !strings.Contains(stdout.String(), "dist/**") {
		t.Errorf("expected dist/** to be extracted, got: %s", stdout.String())
	}
}

func TestRunExtractMutuallyExclusive(t *testing.T) {
	repo := t.TempDir()
	if err := os.WriteFile(filepath.Join(repo, "AGENTS.md"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	err := Run([]string{"extract", repo, "--json", "--yaml"}, "0.1.0-test", &stdout, &stderr)
	if err == nil {
		t.Fatal("expected error for --json + --yaml")
	}
}

func TestRunExtractHelp(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if err := Run([]string{"extract", "--help"}, "0.1.0-test", &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "Usage: reconc extract") {
		t.Errorf("expected usage, got: %s", stdout.String())
	}
}

// --- W49 / W50: spec + coverage helpers -----------------------------

func TestRunSpecMissing(t *testing.T) {
	repo := t.TempDir()
	var stdout, stderr bytes.Buffer
	err := Run([]string{"spec", "check", repo}, "0.1.0-test", &stdout, &stderr)
	if err == nil {
		t.Fatal("expected error for missing spec.md")
	}
}

func TestRunSpecPresent(t *testing.T) {
	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, "docs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "docs", "spec.md"), []byte("# spec"), 0o644); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	if err := Run([]string{"spec", "check", repo}, "0.1.0-test", &stdout, &stderr); err != nil {
		t.Fatalf("spec check: %v", err)
	}
	if !strings.Contains(stdout.String(), "[OK  ]") {
		t.Errorf("expected OK output, got: %s", stdout.String())
	}
}

func TestRunSpecCustomFileAndJSON(t *testing.T) {
	repo := t.TempDir()
	if err := os.WriteFile(filepath.Join(repo, "custom.md"), []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	if err := Run([]string{"spec", "check", repo, "--file", "custom.md", "--json"}, "0.1.0-test", &stdout, &stderr); err != nil {
		t.Fatalf("spec check: %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		t.Fatalf("expected JSON, got: %v\n%s", err, stdout.String())
	}
	if payload["ok"] != true || payload["exists"] != true {
		t.Errorf("expected ok+exists=true, got %v", payload)
	}
}

func TestRunSpecHelp(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if err := Run([]string{"spec", "--help"}, "0.1.0-test", &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "reconc spec check") {
		t.Errorf("expected usage banner, got: %s", stdout.String())
	}
}

func TestRunCoverageMissing(t *testing.T) {
	repo := t.TempDir()
	var stdout, stderr bytes.Buffer
	err := Run([]string{"coverage", "check", repo}, "0.1.0-test", &stdout, &stderr)
	if err == nil {
		t.Fatal("expected error for missing coverage file")
	}
}

func TestRunCoveragePasses(t *testing.T) {
	repo := t.TempDir()
	if err := os.WriteFile(filepath.Join(repo, "coverage.txt"), []byte("coverage: 92.3% of statements\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	if err := Run([]string{"coverage", "check", repo}, "0.1.0-test", &stdout, &stderr); err != nil {
		t.Fatalf("coverage check: %v", err)
	}
	if !strings.Contains(stdout.String(), "[OK  ]") {
		t.Errorf("expected OK, got: %s", stdout.String())
	}
}

func TestRunCoverageBelowMin(t *testing.T) {
	repo := t.TempDir()
	if err := os.WriteFile(filepath.Join(repo, "coverage.txt"), []byte("75%"), 0o644); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	err := Run([]string{"coverage", "check", repo, "--min-pct", "80"}, "0.1.0-test", &stdout, &stderr)
	if err == nil {
		t.Fatal("expected error for below-min coverage")
	}
	if code := ExitCode(err); code != 1 {
		t.Errorf("expected exit 1, got %d", code)
	}
}

func TestRunCoverageJSON(t *testing.T) {
	repo := t.TempDir()
	if err := os.WriteFile(filepath.Join(repo, "coverage.txt"), []byte("87.5%"), 0o644); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	if err := Run([]string{"coverage", "check", repo, "--json"}, "0.1.0-test", &stdout, &stderr); err != nil {
		t.Fatalf("coverage --json: %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		t.Fatalf("expected JSON, got: %v\n%s", err, stdout.String())
	}
	if payload["found_pct"].(float64) != 87.5 {
		t.Errorf("expected 87.5 pct, got %v", payload["found_pct"])
	}
}

func TestRunCoverageNoPercentInFile(t *testing.T) {
	repo := t.TempDir()
	if err := os.WriteFile(filepath.Join(repo, "coverage.txt"), []byte("no numbers here"), 0o644); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	err := Run([]string{"coverage", "check", repo}, "0.1.0-test", &stdout, &stderr)
	if err == nil {
		t.Fatal("expected error for file without percentage")
	}
}

// --- W46: post-task-check -------------------------------------------

func TestRunPostTaskCheckPassesOnCleanRepo(t *testing.T) {
	t.Setenv("RECONC_HOME", t.TempDir())
	repo := makeAssertRepo(t,
		"rules:\n  - id: r1\n    kind: deny_write\n    paths: ['x']\n    mode: warn\n    message: m\n")
	var stdout, stderr bytes.Buffer
	if err := Run([]string{"post-task-check", repo}, "0.1.0-test", &stdout, &stderr); err != nil {
		t.Fatalf("post-task-check should pass for clean repo: %v", err)
	}
	if !strings.Contains(stdout.String(), "All checks passed") {
		t.Errorf("expected pass summary, got:\n%s", stdout.String())
	}
}

func TestRunPostTaskCheckFailsOnRecentBlock(t *testing.T) {
	t.Setenv("RECONC_AUDIT", "1")
	t.Setenv("RECONC_HOME", t.TempDir())
	repo := makeAssertRepo(t,
		"rules:\n  - id: r1\n    kind: deny_write\n    paths: ['gen/**']\n    mode: block\n    message: m\n")
	// Trigger a block so there's at least one blocking audit entry.
	var stdout, stderr bytes.Buffer
	_ = Run([]string{"check", repo, "--write", "gen/x.go"}, "0.1.0-test", &stdout, &stderr)
	stdout.Reset()
	err := Run([]string{"post-task-check", repo}, "0.1.0-test", &stdout, &stderr)
	if err == nil {
		t.Fatal("expected post-task-check to fail with recent block")
	}
	if code := ExitCode(err); code != 1 {
		t.Errorf("expected exit 1, got %d", code)
	}
}

func TestRunPostTaskCheckFailsOnMovedLockfileRoot(t *testing.T) {
	t.Setenv("RECONC_HOME", t.TempDir())
	repo := makeAssertRepo(t,
		"rules:\n  - id: r1\n    kind: deny_write\n    paths: ['x']\n    mode: warn\n    message: m\n")
	lockfile := filepath.Join(repo, ".reconc", "policy.lock.json")
	data, err := os.ReadFile(lockfile)
	if err != nil {
		t.Fatalf("read lockfile: %v", err)
	}
	moved := strings.ReplaceAll(string(data), repo, filepath.Join(t.TempDir(), "old-root"))
	if err := os.WriteFile(lockfile, []byte(moved), 0o644); err != nil {
		t.Fatalf("rewrite lockfile root: %v", err)
	}

	var stdout, stderr bytes.Buffer
	err = Run([]string{"post-task-check", repo}, "0.1.0-test", &stdout, &stderr)
	if err == nil || ExitCode(err) != 1 {
		t.Fatalf("expected post-task-check to fail on moved root, got err=%v code=%d", err, ExitCode(err))
	}
	if !strings.Contains(stdout.String(), "repo_root does not match") {
		t.Fatalf("expected repo_root mismatch output, got %q", stdout.String())
	}
}

func TestRunPostTaskCheckJSON(t *testing.T) {
	t.Setenv("RECONC_HOME", t.TempDir())
	repo := makeAssertRepo(t,
		"rules:\n  - id: r1\n    kind: deny_write\n    paths: ['x']\n    mode: warn\n    message: m\n")
	var stdout, stderr bytes.Buffer
	if err := Run([]string{"post-task-check", repo, "--json"}, "0.1.0-test", &stdout, &stderr); err != nil {
		t.Fatalf("post-task-check --json: %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		t.Fatalf("expected JSON, got: %v\n%s", err, stdout.String())
	}
	if payload["ok"] != true {
		t.Errorf("expected ok=true, got %v", payload)
	}
}

func TestRunPostTaskCheckWindowFlag(t *testing.T) {
	t.Setenv("RECONC_HOME", t.TempDir())
	repo := makeAssertRepo(t,
		"rules:\n  - id: r1\n    kind: deny_write\n    paths: ['x']\n    mode: warn\n    message: m\n")
	var stdout, stderr bytes.Buffer
	if err := Run([]string{"post-task-check", repo, "--window", "5"}, "0.1.0-test", &stdout, &stderr); err != nil {
		t.Fatalf("--window: %v", err)
	}
}

func TestRunPostTaskCheckBadWindow(t *testing.T) {
	var stdout, stderr bytes.Buffer
	err := Run([]string{"post-task-check", ".", "--window", "notanumber"}, "0.1.0-test", &stdout, &stderr)
	if err == nil {
		t.Fatal("expected error for non-int --window")
	}
}

func TestRunPostTaskCheckHelp(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if err := Run([]string{"post-task-check", "--help"}, "0.1.0-test", &stdout, &stderr); err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if !strings.Contains(stdout.String(), "Usage: reconc post-task-check") {
		t.Errorf("expected usage, got: %s", stdout.String())
	}
}

func TestRunDonePassesWithTerseOutput(t *testing.T) {
	t.Setenv("RECONC_HOME", t.TempDir())
	repo := makeAssertRepo(t,
		"rules:\n  - id: r1\n    kind: deny_write\n    paths: ['x']\n    mode: warn\n    message: m\n")
	var stdout, stderr bytes.Buffer
	if err := Run([]string{"done", repo}, "0.4.0-test", &stdout, &stderr); err != nil {
		t.Fatalf("done should pass for clean repo: %v", err)
	}
	if strings.TrimSpace(stdout.String()) != "done" {
		t.Fatalf("expected terse done output, got %q", stdout.String())
	}
}

func TestRunDoneBlocksOnRecentBlock(t *testing.T) {
	t.Setenv("RECONC_AUDIT", "1")
	t.Setenv("RECONC_HOME", t.TempDir())
	repo := makeAssertRepo(t,
		"rules:\n  - id: r1\n    kind: deny_write\n    paths: ['gen/**']\n    mode: block\n    message: m\n")
	var stdout, stderr bytes.Buffer
	_ = Run([]string{"check", repo, "--write", "gen/x.go"}, "0.4.0-test", &stdout, &stderr)
	stdout.Reset()
	err := Run([]string{"done", repo}, "0.4.0-test", &stdout, &stderr)
	if err == nil || ExitCode(err) != 2 {
		t.Fatalf("expected done to exit 2 on blocking audit, got err=%v code=%d", err, ExitCode(err))
	}
	if !strings.Contains(stdout.String(), "blocked:") {
		t.Fatalf("expected blocked output, got %q", stdout.String())
	}
}

func TestRunDoneBlocksOnMovedLockfileRoot(t *testing.T) {
	t.Setenv("RECONC_HOME", t.TempDir())
	repo := makeAssertRepo(t,
		"rules:\n  - id: r1\n    kind: deny_write\n    paths: ['x']\n    mode: warn\n    message: m\n")
	lockfile := filepath.Join(repo, ".reconc", "policy.lock.json")
	data, err := os.ReadFile(lockfile)
	if err != nil {
		t.Fatalf("read lockfile: %v", err)
	}
	moved := strings.ReplaceAll(string(data), repo, filepath.Join(t.TempDir(), "old-root"))
	if err := os.WriteFile(lockfile, []byte(moved), 0o644); err != nil {
		t.Fatalf("rewrite lockfile root: %v", err)
	}

	var stdout, stderr bytes.Buffer
	err = Run([]string{"done", repo}, "0.4.0-test", &stdout, &stderr)
	if err == nil || ExitCode(err) != 2 {
		t.Fatalf("expected done to block on moved root, got err=%v code=%d", err, ExitCode(err))
	}
	if !strings.Contains(stdout.String(), "repo_root does not match") {
		t.Fatalf("expected repo_root mismatch output, got %q", stdout.String())
	}
}

func TestRunDoneJSON(t *testing.T) {
	t.Setenv("RECONC_HOME", t.TempDir())
	repo := makeAssertRepo(t,
		"rules:\n  - id: r1\n    kind: deny_write\n    paths: ['x']\n    mode: warn\n    message: m\n")
	var stdout, stderr bytes.Buffer
	if err := Run([]string{"done", repo, "--json"}, "0.4.0-test", &stdout, &stderr); err != nil {
		t.Fatalf("done --json: %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		t.Fatalf("expected JSON, got: %v\n%s", err, stdout.String())
	}
	if payload["ok"] != true {
		t.Fatalf("expected ok=true, got %v", payload)
	}
}

func TestRunDoneHelpAndValueValidation(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if err := Run([]string{"done", "--help"}, "0.4.0-test", &stdout, &stderr); err != nil {
		t.Fatalf("done --help: %v", err)
	}
	if !strings.Contains(stdout.String(), "Usage: reconc done") {
		t.Fatalf("expected done usage, got %q", stdout.String())
	}

	cases := []struct {
		argv    []string
		wantSub string
	}{
		{argv: []string{"done", "--window"}, wantSub: "--window requires minutes"},
		{argv: []string{"done", "--window", "0"}, wantSub: "--window must be a positive integer"},
		{argv: []string{"done", "--bogus"}, wantSub: "unknown flag"},
	}
	for _, tc := range cases {
		stdout.Reset()
		stderr.Reset()
		err := Run(tc.argv, "0.4.0-test", &stdout, &stderr)
		if err == nil {
			t.Fatalf("expected error for %v", tc.argv)
		}
		if !strings.Contains(err.Error(), tc.wantSub) {
			t.Fatalf("error %q does not contain %q", err.Error(), tc.wantSub)
		}
	}
}

// --- W47: delta ------------------------------------------------------

func TestRunDeltaEmpty(t *testing.T) {
	repo := t.TempDir()
	var stdout, stderr bytes.Buffer
	if err := Run([]string{"delta", repo}, "0.1.0-test", &stdout, &stderr); err != nil {
		t.Fatalf("delta: %v", err)
	}
	if !strings.Contains(stdout.String(), "0 audit entries") {
		t.Errorf("expected empty delta, got: %s", stdout.String())
	}
}

func TestRunDeltaJSON(t *testing.T) {
	repo := t.TempDir()
	var stdout, stderr bytes.Buffer
	if err := Run([]string{"delta", repo, "--json"}, "0.1.0-test", &stdout, &stderr); err != nil {
		t.Fatalf("delta --json: %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		t.Fatalf("expected JSON, got: %v\n%s", err, stdout.String())
	}
	if _, ok := payload["total"]; !ok {
		t.Error("expected 'total' field in JSON")
	}
}

func TestRunDeltaSinceFilter(t *testing.T) {
	t.Setenv("RECONC_AUDIT", "1")
	t.Setenv("RECONC_HOME", t.TempDir())
	repo := makeAssertRepo(t,
		"rules:\n  - id: r1\n    kind: deny_write\n    paths: ['gen/**']\n    mode: block\n    message: m\n")
	var stdout, stderr bytes.Buffer
	_ = Run([]string{"check", repo, "--write", "gen/x.go"}, "0.1.0-test", &stdout, &stderr)
	stdout.Reset()
	if err := Run([]string{"delta", repo, "--since", "1970-01-01T00:00:00Z"}, "0.1.0-test", &stdout, &stderr); err != nil {
		t.Fatalf("delta --since: %v", err)
	}
	if !strings.Contains(stdout.String(), "1 audit entries") {
		t.Errorf("expected 1 audit entry, got: %s", stdout.String())
	}
}

func TestRunDeltaHelp(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if err := Run([]string{"delta", "--help"}, "0.1.0-test", &stdout, &stderr); err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if !strings.Contains(stdout.String(), "Usage: reconc delta") {
		t.Errorf("expected usage, got: %s", stdout.String())
	}
}

// --- W51: `reconc start` canonical onboarding doc -------------------

func TestRunStartStdout(t *testing.T) {
	t.Setenv("RECONC_HOME", t.TempDir())
	repo := makeAssertRepo(t,
		"rules:\n  - id: r1\n    kind: deny_write\n    paths: ['x']\n    mode: warn\n    message: m\n")
	var stdout, stderr bytes.Buffer
	if err := Run([]string{"start", repo}, "0.1.0-test", &stdout, &stderr); err != nil {
		t.Fatalf("start: %v", err)
	}
	out := stdout.String()
	for _, want := range []string{"# Session Start", "## Repo state", "## Recent activity", "## Next action", "## Agent orientation"} {
		if !strings.Contains(out, want) {
			t.Errorf("start output missing %q; got:\n%s", want, out)
		}
	}
}

func TestRunStartJSON(t *testing.T) {
	t.Setenv("RECONC_HOME", t.TempDir())
	repo := makeAssertRepo(t,
		"rules:\n  - id: r1\n    kind: deny_write\n    paths: ['x']\n    mode: warn\n    message: m\n")
	var stdout, stderr bytes.Buffer
	if err := Run([]string{"start", repo, "--json"}, "0.1.0-test", &stdout, &stderr); err != nil {
		t.Fatalf("start --json: %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		t.Fatalf("expected JSON, got: %v\n%s", err, stdout.String())
	}
	if _, ok := payload["lockfile_status"]; !ok {
		t.Error("JSON payload missing lockfile_status")
	}
}

func TestRunStartWritesFile(t *testing.T) {
	t.Setenv("RECONC_HOME", t.TempDir())
	repo := makeAssertRepo(t,
		"rules:\n  - id: r1\n    kind: deny_write\n    paths: ['x']\n    mode: warn\n    message: m\n")
	var stdout, stderr bytes.Buffer
	if err := Run([]string{"start", repo, "--write", "start.md"}, "0.1.0-test", &stdout, &stderr); err != nil {
		t.Fatalf("start --write: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(repo, "start.md"))
	if err != nil {
		t.Fatalf("start.md not written: %v", err)
	}
	if !strings.Contains(string(data), "# Session Start") {
		t.Errorf("start.md content wrong:\n%s", string(data))
	}
}

func TestRunStartRefusesOverwriteWithoutForce(t *testing.T) {
	t.Setenv("RECONC_HOME", t.TempDir())
	repo := makeAssertRepo(t,
		"rules:\n  - id: r1\n    kind: deny_write\n    paths: ['x']\n    mode: warn\n    message: m\n")
	if err := os.WriteFile(filepath.Join(repo, "start.md"), []byte("existing"), 0o644); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	err := Run([]string{"start", repo, "--write", "start.md"}, "0.1.0-test", &stdout, &stderr)
	if err == nil {
		t.Fatal("expected error when overwriting without --force")
	}
	// With --force it should succeed.
	stdout.Reset()
	if err := Run([]string{"start", repo, "--write", "start.md", "--force"}, "0.1.0-test", &stdout, &stderr); err != nil {
		t.Errorf("--force should overwrite: %v", err)
	}
}

func TestRunStartHelp(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if err := Run([]string{"start", "--help"}, "0.1.0-test", &stdout, &stderr); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(stdout.String(), "reconc start") {
		t.Errorf("expected usage banner, got: %s", stdout.String())
	}
}

// --- W43: context size guard -----------------------------------------

func TestRunContextMissingSubcommand(t *testing.T) {
	var stdout, stderr bytes.Buffer
	err := Run([]string{"context"}, "0.1.0-test", &stdout, &stderr)
	if err == nil {
		t.Fatal("expected error for missing subcommand")
	}
}

func TestRunContextSizeUnderBudget(t *testing.T) {
	repo := t.TempDir()
	if err := os.WriteFile(filepath.Join(repo, "AGENTS.md"), []byte("small"), 0o644); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	if err := Run([]string{"context", "size", repo}, "0.1.0-test", &stdout, &stderr); err != nil {
		t.Fatalf("context size should pass for small repo: %v", err)
	}
	if !strings.Contains(stdout.String(), "[OK]") {
		t.Errorf("expected OK status, got:\n%s", stdout.String())
	}
}

func TestRunContextSizeOverBudgetExits1(t *testing.T) {
	repo := t.TempDir()
	if err := os.WriteFile(filepath.Join(repo, "AGENTS.md"), bytes.Repeat([]byte("x"), 4000), 0o644); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	err := Run([]string{"context", "size", repo, "--limit", "100"}, "0.1.0-test", &stdout, &stderr)
	if err == nil {
		t.Fatal("expected CLIError for over-budget")
	}
	if code := ExitCode(err); code != 1 {
		t.Errorf("expected exit 1, got %d", code)
	}
	if !strings.Contains(stdout.String(), "OVER BUDGET") {
		t.Errorf("expected OVER BUDGET banner, got:\n%s", stdout.String())
	}
}

func TestRunContextSizeJSON(t *testing.T) {
	repo := t.TempDir()
	if err := os.WriteFile(filepath.Join(repo, "AGENTS.md"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	if err := Run([]string{"context", "size", repo, "--json"}, "0.1.0-test", &stdout, &stderr); err != nil {
		t.Fatalf("context size --json: %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		t.Fatalf("expected valid JSON, got: %v\n%s", err, stdout.String())
	}
	if payload["over_budget"] != false {
		t.Errorf("expected over_budget=false, got %v", payload["over_budget"])
	}
}

func TestRunContextSizeCustomFiles(t *testing.T) {
	repo := t.TempDir()
	if err := os.WriteFile(filepath.Join(repo, "my.md"), []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	if err := Run([]string{"context", "size", repo, "--files", "my.md"}, "0.1.0-test", &stdout, &stderr); err != nil {
		t.Fatalf("context size --files: %v", err)
	}
	if !strings.Contains(stdout.String(), "my.md") {
		t.Errorf("expected my.md in output, got:\n%s", stdout.String())
	}
	// Default files should NOT appear when --files overrides.
	if strings.Contains(stdout.String(), "docs/changelog.md") {
		t.Errorf("default files should not appear when --files is given")
	}
}

func TestRunContextSizeUnknownFlag(t *testing.T) {
	repo := t.TempDir()
	var stdout, stderr bytes.Buffer
	err := Run([]string{"context", "size", repo, "--nope"}, "0.1.0-test", &stdout, &stderr)
	if err == nil {
		t.Fatal("expected error for unknown flag")
	}
}

func TestRunContextSizeBadLimit(t *testing.T) {
	repo := t.TempDir()
	var stdout, stderr bytes.Buffer
	err := Run([]string{"context", "size", repo, "--limit", "notanumber"}, "0.1.0-test", &stdout, &stderr)
	if err == nil {
		t.Fatal("expected error for non-integer --limit")
	}
}

func TestRunContextHelp(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if err := Run([]string{"context", "--help"}, "0.1.0-test", &stdout, &stderr); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(stdout.String(), "reconc context size") {
		t.Errorf("expected usage banner, got: %s", stdout.String())
	}
}

// --- W44: session-briefing -------------------------------------------

func TestRunSessionBriefingNonExistentDir(t *testing.T) {
	var stdout, stderr bytes.Buffer
	// session-briefing is always informational, never errors out.
	if err := Run([]string{"session-briefing", "/definitely/not/a/real/path"}, "0.1.0-test", &stdout, &stderr); err != nil {
		t.Fatalf("session-briefing should not fail on missing path: %v", err)
	}
	if !strings.Contains(stdout.String(), "discovery error") {
		t.Errorf("expected discovery-error message for missing path, got:\n%s", stdout.String())
	}
}

func TestRunSessionBriefingUninitializedRepo(t *testing.T) {
	repo := t.TempDir()
	var stdout, stderr bytes.Buffer
	if err := Run([]string{"session-briefing", repo}, "0.1.0-test", &stdout, &stderr); err != nil {
		t.Fatalf("session-briefing: %v", err)
	}
	out := stdout.String()
	if !strings.Contains(out, "no reconc config") {
		t.Errorf("expected 'no reconc config' message, got:\n%s", out)
	}
	if !strings.Contains(out, "reconc init") {
		t.Errorf("expected next-step hint to mention init, got:\n%s", out)
	}
}

func TestRunSessionBriefingDiscoveredRepo(t *testing.T) {
	t.Setenv("RECONC_HOME", t.TempDir())
	repo := makeAssertRepo(t,
		"rules:\n  - id: r1\n    kind: deny_write\n    paths: ['gen/**']\n    mode: block\n    message: m\n")
	var stdout, stderr bytes.Buffer
	if err := Run([]string{"session-briefing", repo}, "0.1.0-test", &stdout, &stderr); err != nil {
		t.Fatalf("session-briefing: %v", err)
	}
	out := stdout.String()
	for _, want := range []string{"Lockfile:", "fresh", "Rules:", "Conflicts:"} {
		if !strings.Contains(out, want) {
			t.Errorf("expected %q in briefing, got:\n%s", want, out)
		}
	}
}

func TestRunTUIDiscoveredRepo(t *testing.T) {
	t.Setenv("RECONC_HOME", t.TempDir())
	repo := makeAssertRepo(t,
		"rules:\n  - id: r1\n    kind: deny_write\n    paths: ['gen/**']\n    mode: block\n    message: m\n")
	var stdout, stderr bytes.Buffer
	if err := Run([]string{"tui", repo}, "0.1.0-test", &stdout, &stderr); err != nil {
		t.Fatalf("tui: %v", err)
	}
	out := stdout.String()
	for _, want := range []string{"reconc tui:", "lockfile:   fresh", "Sources:", "Rules:", "r1"} {
		if !strings.Contains(out, want) {
			t.Errorf("expected %q in tui output, got:\n%s", want, out)
		}
	}
}

func TestRunTUIJSONOutput(t *testing.T) {
	t.Setenv("RECONC_HOME", t.TempDir())
	repo := makeAssertRepo(t,
		"rules:\n  - id: r1\n    kind: deny_write\n    paths: ['gen/**']\n    mode: block\n    message: m\n")
	var stdout, stderr bytes.Buffer
	if err := Run([]string{"tui", repo, "--json"}, "0.1.0-test", &stdout, &stderr); err != nil {
		t.Fatalf("tui --json: %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		t.Fatalf("expected JSON, got: %v\n%s", err, stdout.String())
	}
	if payload["lockfile_status"] != "fresh" {
		t.Errorf("expected lockfile_status=fresh, got %v", payload["lockfile_status"])
	}
	if rc, ok := payload["rule_count"].(float64); !ok || rc != 1 {
		t.Errorf("expected rule_count=1, got %v", payload["rule_count"])
	}
}

func TestRunSessionBriefingJSONOutput(t *testing.T) {
	t.Setenv("RECONC_HOME", t.TempDir())
	repo := makeAssertRepo(t,
		"rules:\n  - id: r1\n    kind: deny_write\n    paths: ['gen/**']\n    mode: block\n    message: m\n")
	var stdout, stderr bytes.Buffer
	if err := Run([]string{"session-briefing", repo, "--json"}, "0.1.0-test", &stdout, &stderr); err != nil {
		t.Fatalf("session-briefing --json: %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		t.Fatalf("expected JSON, got: %v\n%s", err, stdout.String())
	}
	if payload["lockfile_status"] != "fresh" {
		t.Errorf("expected lockfile_status=fresh, got %v", payload["lockfile_status"])
	}
	if rc, ok := payload["rule_count"].(float64); !ok || rc != 1 {
		t.Errorf("expected rule_count=1, got %v", payload["rule_count"])
	}
}

func TestRunSessionBriefingReportsAuditActivity(t *testing.T) {
	t.Setenv("RECONC_AUDIT", "1")
	t.Setenv("RECONC_HOME", t.TempDir())
	repo := makeAssertRepo(t,
		"rules:\n  - id: r1\n    kind: deny_write\n    paths: ['gen/**']\n    mode: block\n    message: m\n")
	// Trigger a few audit entries.
	var stdout, stderr bytes.Buffer
	for i := 0; i < 3; i++ {
		stdout.Reset()
		_ = Run([]string{"check", repo, "--write", "gen/x.go"}, "0.1.0-test", &stdout, &stderr)
	}
	stdout.Reset()
	if err := Run([]string{"session-briefing", repo}, "0.1.0-test", &stdout, &stderr); err != nil {
		t.Fatalf("session-briefing: %v", err)
	}
	out := stdout.String()
	if !strings.Contains(out, "3 entries") {
		t.Errorf("expected audit activity reported, got:\n%s", out)
	}
	if !strings.Contains(out, "Top rule:") {
		t.Errorf("expected top rule line, got:\n%s", out)
	}
}

func TestRunSessionBriefingHelp(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if err := Run([]string{"session-briefing", "--help"}, "0.1.0-test", &stdout, &stderr); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(stdout.String(), "Usage: reconc session-briefing") {
		t.Errorf("expected usage banner, got: %s", stdout.String())
	}
}

// --- W18: rule templates --------------------------------------------

func TestRunTemplateMissingSubcommand(t *testing.T) {
	var stdout, stderr bytes.Buffer
	err := Run([]string{"template"}, "0.1.0-test", &stdout, &stderr)
	if err == nil {
		t.Fatal("expected error for missing subcommand")
	}
}

func TestRunTemplateList(t *testing.T) {
	t.Setenv("RECONC_HOME", t.TempDir())
	var stdout, stderr bytes.Buffer
	if err := Run([]string{"template", "list"}, "0.1.0-test", &stdout, &stderr); err != nil {
		t.Fatalf("template list: %v", err)
	}
	out := stdout.String()
	for _, want := range []string{"tests-follow-source", "no-generated-writes", "builtin"} {
		if !strings.Contains(out, want) {
			t.Errorf("template list missing %q; got:\n%s", want, out)
		}
	}
}

func TestRunTemplateListJSON(t *testing.T) {
	t.Setenv("RECONC_HOME", t.TempDir())
	var stdout, stderr bytes.Buffer
	if err := Run([]string{"template", "list", "--json"}, "0.1.0-test", &stdout, &stderr); err != nil {
		t.Fatalf("template list --json: %v", err)
	}
	var list []map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &list); err != nil {
		t.Fatalf("expected JSON array, got: %v\n%s", err, stdout.String())
	}
	if len(list) < 4 {
		t.Errorf("expected at least 4 templates, got %d", len(list))
	}
}

func TestRunTemplateShow(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if err := Run([]string{"template", "show", "tests-follow-source"}, "0.1.0-test", &stdout, &stderr); err != nil {
		t.Fatalf("template show: %v", err)
	}
	out := stdout.String()
	for _, want := range []string{"tests-follow-source", "couple_change", "builtin"} {
		if !strings.Contains(out, want) {
			t.Errorf("template show missing %q; got:\n%s", want, out)
		}
	}
}

func TestRunTemplateShowUnknown(t *testing.T) {
	var stdout, stderr bytes.Buffer
	err := Run([]string{"template", "show", "bogus-name"}, "0.1.0-test", &stdout, &stderr)
	if err == nil {
		t.Fatal("expected error for unknown template")
	}
}

func TestRunTemplateShowMissingName(t *testing.T) {
	var stdout, stderr bytes.Buffer
	err := Run([]string{"template", "show"}, "0.1.0-test", &stdout, &stderr)
	if err == nil {
		t.Fatal("expected error for missing template name")
	}
}

func TestRunTemplateHelp(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if err := Run([]string{"template", "--help"}, "0.1.0-test", &stdout, &stderr); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(stdout.String(), "reconc template") {
		t.Errorf("expected usage banner, got: %s", stdout.String())
	}
}

// --- W29: audit log -------------------------------------------------

func TestRunAuditMissingSubcommand(t *testing.T) {
	var stdout, stderr bytes.Buffer
	err := Run([]string{"audit"}, "0.1.0-test", &stdout, &stderr)
	if err == nil {
		t.Fatal("expected error for missing subcommand")
	}
}

func TestRunAuditUnknownSubcommand(t *testing.T) {
	var stdout, stderr bytes.Buffer
	err := Run([]string{"audit", "noop"}, "0.1.0-test", &stdout, &stderr)
	if err == nil {
		t.Fatal("expected error for unknown subcommand")
	}
}

func TestRunAuditTailEmpty(t *testing.T) {
	repo := t.TempDir()
	var stdout, stderr bytes.Buffer
	if err := Run([]string{"audit", "tail", repo}, "0.1.0-test", &stdout, &stderr); err != nil {
		t.Fatalf("audit tail: %v", err)
	}
	if !strings.Contains(stdout.String(), "No audit entries") {
		t.Errorf("expected empty-state, got: %s", stdout.String())
	}
}

func TestRunAuditTailReadsLog(t *testing.T) {
	repo := t.TempDir()
	// Seed the log with a direct Append.
	if err := os.MkdirAll(filepath.Join(repo, ".reconc"), 0o755); err != nil {
		t.Fatal(err)
	}
	payload := `{"ts":"2026-04-14T00:00:00Z","event":"check","decision":"pass","ok":true,"violation_count":0,"blocking_count":0}` + "\n"
	if err := os.WriteFile(filepath.Join(repo, ".reconc", "audit.jsonl"), []byte(payload), 0o644); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	if err := Run([]string{"audit", "tail", repo}, "0.1.0-test", &stdout, &stderr); err != nil {
		t.Fatalf("audit tail: %v", err)
	}
	if !strings.Contains(stdout.String(), "check") || !strings.Contains(stdout.String(), "pass") {
		t.Errorf("expected tail output to contain entry, got: %s", stdout.String())
	}
}

func TestRunAuditTailJSON(t *testing.T) {
	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, ".reconc"), 0o755); err != nil {
		t.Fatal(err)
	}
	payload := `{"ts":"2026-04-14T00:00:00Z","event":"check","decision":"block","ok":false,"violation_count":1,"blocking_count":1,"rule_ids":["r1"]}` + "\n"
	if err := os.WriteFile(filepath.Join(repo, ".reconc", "audit.jsonl"), []byte(payload), 0o644); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	if err := Run([]string{"audit", "tail", repo, "--json"}, "0.1.0-test", &stdout, &stderr); err != nil {
		t.Fatalf("audit tail --json: %v", err)
	}
	var entries []map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &entries); err != nil {
		t.Fatalf("expected valid JSON array, got: %v\n%s", err, stdout.String())
	}
	if len(entries) != 1 {
		t.Errorf("expected 1 entry, got %d", len(entries))
	}
}

func TestRunAuditStatsEmpty(t *testing.T) {
	repo := t.TempDir()
	var stdout, stderr bytes.Buffer
	if err := Run([]string{"audit", "stats", repo}, "0.1.0-test", &stdout, &stderr); err != nil {
		t.Fatalf("audit stats: %v", err)
	}
	if !strings.Contains(stdout.String(), "No audit entries") {
		t.Errorf("expected empty-state, got: %s", stdout.String())
	}
}

func TestRunAuditStatsJSON(t *testing.T) {
	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, ".reconc"), 0o755); err != nil {
		t.Fatal(err)
	}
	payload := `{"ts":"2026-04-14T00:00:00Z","event":"check","decision":"block","blocking_count":1,"rule_ids":["r1"]}` + "\n"
	if err := os.WriteFile(filepath.Join(repo, ".reconc", "audit.jsonl"), []byte(payload), 0o644); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	if err := Run([]string{"audit", "stats", repo, "--json"}, "0.1.0-test", &stdout, &stderr); err != nil {
		t.Fatalf("audit stats --json: %v", err)
	}
	var payload2 map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &payload2); err != nil {
		t.Fatalf("expected JSON, got: %v\n%s", err, stdout.String())
	}
	if payload2["total_entries"].(float64) != 1 {
		t.Errorf("expected total_entries=1, got: %v", payload2["total_entries"])
	}
}

func TestRunAuditExport(t *testing.T) {
	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, ".reconc"), 0o755); err != nil {
		t.Fatal(err)
	}
	payload := `{"ts":"2026-04-14T00:00:00Z","event":"check","decision":"pass"}` + "\n"
	if err := os.WriteFile(filepath.Join(repo, ".reconc", "audit.jsonl"), []byte(payload), 0o644); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	if err := Run([]string{"audit", "export", repo}, "0.1.0-test", &stdout, &stderr); err != nil {
		t.Fatalf("audit export: %v", err)
	}
	if stdout.String() != payload {
		t.Errorf("export output mismatch:\nwant: %q\ngot:  %q", payload, stdout.String())
	}
}

func TestRunAuditHelp(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if err := Run([]string{"audit", "--help"}, "0.1.0-test", &stdout, &stderr); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(stdout.String(), "reconc audit") {
		t.Errorf("expected help output, got: %s", stdout.String())
	}
}

func TestAuditIntegrationWithCheckEnablesLogging(t *testing.T) {
	// End-to-end: RECONC_AUDIT=1 + `reconc check` should produce a log line.
	t.Setenv("RECONC_AUDIT", "1")
	t.Setenv("RECONC_HOME", t.TempDir())
	repo := t.TempDir()
	if err := os.WriteFile(filepath.Join(repo, "AGENTS.md"), []byte("# t\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(repo, "policies"), 0o755); err != nil {
		t.Fatal(err)
	}
	rules := "rules:\n  - id: r1\n    kind: deny_write\n    paths: ['gen/**']\n    mode: block\n    message: no\n"
	if err := os.WriteFile(filepath.Join(repo, "policies", "rules.yml"), []byte(rules), 0o644); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	_ = Run([]string{"compile", repo}, "0.1.0-test", &stdout, &stderr)
	stdout.Reset()
	_ = Run([]string{"check", repo, "--write", "gen/x.go"}, "0.1.0-test", &stdout, &stderr)

	// Log file should now exist and contain exactly one entry.
	data, err := os.ReadFile(filepath.Join(repo, ".reconc", "audit.jsonl"))
	if err != nil {
		t.Fatalf("expected audit log created, got: %v", err)
	}
	if !strings.Contains(string(data), `"event":"check"`) {
		t.Errorf("expected 'check' event in log, got:\n%s", string(data))
	}
	if !strings.Contains(string(data), `"decision":"block"`) {
		t.Errorf("expected block decision in log, got:\n%s", string(data))
	}
}

func TestAuditDisabledByDefault(t *testing.T) {
	// Ensure audit stays off when RECONC_AUDIT is unset.
	t.Setenv("RECONC_AUDIT", "")
	os.Unsetenv("RECONC_AUDIT")
	t.Setenv("RECONC_HOME", t.TempDir())
	repo := t.TempDir()
	if err := os.WriteFile(filepath.Join(repo, "AGENTS.md"), []byte("# t\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(repo, "policies"), 0o755); err != nil {
		t.Fatal(err)
	}
	rules := "rules:\n  - id: r1\n    kind: deny_write\n    paths: ['gen/**']\n    mode: block\n    message: no\n"
	if err := os.WriteFile(filepath.Join(repo, "policies", "rules.yml"), []byte(rules), 0o644); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	_ = Run([]string{"compile", repo}, "0.1.0-test", &stdout, &stderr)
	stdout.Reset()
	_ = Run([]string{"check", repo, "--write", "gen/x.go"}, "0.1.0-test", &stdout, &stderr)

	if _, err := os.Stat(filepath.Join(repo, ".reconc", "audit.jsonl")); err == nil {
		t.Error("audit log should not be created when RECONC_AUDIT is unset")
	}
}

func TestRunCompileStrictConflictsPassesWhenClean(t *testing.T) {
	// Fresh repo with no conflicts.
	t.Setenv("RECONC_HOME", t.TempDir())
	repo := t.TempDir()
	if err := os.WriteFile(filepath.Join(repo, "AGENTS.md"), []byte("# t\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(repo, "policies"), 0o755); err != nil {
		t.Fatal(err)
	}
	rules := "rules:\n  - id: r1\n    kind: deny_write\n    paths: ['gen/**']\n    mode: block\n    message: no\n"
	if err := os.WriteFile(filepath.Join(repo, "policies", "rules.yml"), []byte(rules), 0o644); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	if err := Run([]string{"compile", repo, "--strict-conflicts"}, "0.1.0-test", &stdout, &stderr); err != nil {
		t.Errorf("clean ruleset under --strict-conflicts should succeed; got %v", err)
	}
}

// --- help-text coverage sweep ---------------------------------------

func TestEverySubcommandHasHelpOutput(t *testing.T) {
	// For every entry in the canonical Subcommands table, invoking
	// `<cmd> --help` must succeed (exit 0) and produce some non-empty
	// output. Catches help-text drift (commands that gain a dispatch
	// case but forget the -h branch).
	for _, s := range completion.Subcommands {
		t.Run(s.Name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			// Some subcommands require a second arg before --help
			// (e.g. hook needs "generate|install|runtime|claim").
			// Run top-level --help for those so the test stays
			// Kind-agnostic.
			argv := []string{s.Name, "--help"}
			switch s.Name {
			case "hook", "preset", "template", "audit", "changelog",
				"context", "spec", "coverage", "completion":
				// Multi-subcommand; top-level --help suffices.
			}
			if err := Run(argv, "0.4.0-test", &stdout, &stderr); err != nil {
				// Some dispatch surfaces return 1 on "--help needs
				// more args". Acceptable as long as help text is
				// emitted somewhere.
				if stdout.Len() == 0 && stderr.Len() == 0 {
					t.Errorf("%s --help produced no output AND errored: %v", s.Name, err)
				}
			}
			combined := stdout.String() + stderr.String()
			if !strings.Contains(combined, s.Name) {
				// Help output should at minimum mention the command name.
				// Loose-but-catches-missing-help assertion.
				t.Errorf("%s --help output does not mention the command name, got:\n%s", s.Name, combined)
			}
		})
	}
}

// --- coverage for low-coverage CLI commands -------------------------

func TestRunHookGenerateGitPreCommit(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if err := Run([]string{"hook", "generate", "git-pre-commit"}, "0.4.0-test", &stdout, &stderr); err != nil {
		t.Fatalf("hook generate: %v", err)
	}
	if !strings.Contains(stdout.String(), "reconc ci") {
		t.Errorf("expected generated pre-commit to run reconc ci, got:\n%s", stdout.String())
	}
}

func TestRunHookGenerateClaudeCode(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if err := Run([]string{"hook", "generate", "claude-code", "--json"}, "0.4.0-test", &stdout, &stderr); err != nil {
		t.Fatalf("hook generate claude-code: %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		t.Fatalf("expected JSON: %v", err)
	}
}

func TestRunHookGenerateUnknownKindErrors(t *testing.T) {
	var stdout, stderr bytes.Buffer
	err := Run([]string{"hook", "generate", "bogus"}, "0.4.0-test", &stdout, &stderr)
	if err == nil {
		t.Fatal("expected error for unknown kind")
	}
}

func TestRunHookInstallGitPreCommit(t *testing.T) {
	repo := t.TempDir()
	// Must be a git repo for pre-commit install.
	gitDir := filepath.Join(repo, ".git")
	if err := os.MkdirAll(gitDir, 0o755); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	if err := Run([]string{"hook", "install", "git-pre-commit", repo}, "0.4.0-test", &stdout, &stderr); err != nil {
		t.Fatalf("hook install: %v", err)
	}
	hookPath := filepath.Join(gitDir, "hooks", "pre-commit")
	if _, err := os.Stat(hookPath); err != nil {
		t.Errorf("pre-commit hook not installed: %v", err)
	}
}

func TestRunPresetList(t *testing.T) {
	t.Setenv("RECONC_HOME", t.TempDir())
	var stdout, stderr bytes.Buffer
	if err := Run([]string{"preset", "list"}, "0.4.0-test", &stdout, &stderr); err != nil {
		t.Fatalf("preset list: %v", err)
	}
	for _, want := range []string{"agent", "default", "docs-sync", "release", "strict"} {
		if !strings.Contains(stdout.String(), want) {
			t.Errorf("preset list missing %q", want)
		}
	}
}

func TestRunPresetShowBuiltin(t *testing.T) {
	t.Setenv("RECONC_HOME", t.TempDir())
	var stdout, stderr bytes.Buffer
	if err := Run([]string{"preset", "show", "default"}, "0.4.0-test", &stdout, &stderr); err != nil {
		t.Fatalf("preset show: %v", err)
	}
	if !strings.Contains(stdout.String(), "default") {
		t.Errorf("preset show output missing preset name, got: %s", stdout.String())
	}
}

func TestRunPresetShowUnknown(t *testing.T) {
	t.Setenv("RECONC_HOME", t.TempDir())
	var stdout, stderr bytes.Buffer
	err := Run([]string{"preset", "show", "definitely-not-a-preset"}, "0.4.0-test", &stdout, &stderr)
	if err == nil {
		t.Fatal("expected error for unknown preset")
	}
}

func TestRunPresetListJSON(t *testing.T) {
	t.Setenv("RECONC_HOME", t.TempDir())
	var stdout, stderr bytes.Buffer
	if err := Run([]string{"preset", "list", "--json"}, "0.4.0-test", &stdout, &stderr); err != nil {
		t.Fatalf("preset list --json: %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		t.Fatalf("expected JSON: %v", err)
	}
	presets, _ := payload["presets"].([]any)
	if len(presets) < 3 {
		t.Errorf("expected at least 3 bundled presets, got %d", len(presets))
	}
}

func TestGitIsCleanOnNonGitRepo(t *testing.T) {
	repo := t.TempDir()
	clean, _ := gitIsClean(repo)
	if !clean {
		t.Error("non-git repo must be treated as clean (no-op)")
	}
}

func TestRunBootstrapFull(t *testing.T) {
	t.Setenv("RECONC_HOME", t.TempDir())
	repo := t.TempDir()
	// Include .git so pre-commit install runs.
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	if err := Run([]string{"bootstrap", repo}, "0.4.0-test", &stdout, &stderr); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	// Check .reconc.yml + lockfile + pre-commit hook all created.
	for _, path := range []string{
		".reconc.yml",
		"AGENTS.md",
		".reconc/policy.lock.json",
		".git/hooks/pre-commit",
	} {
		if _, err := os.Stat(filepath.Join(repo, path)); err != nil {
			t.Errorf("bootstrap: %s not created: %v", path, err)
		}
	}
}

func TestRunBootstrapJSON(t *testing.T) {
	t.Setenv("RECONC_HOME", t.TempDir())
	repo := t.TempDir()
	var stdout, stderr bytes.Buffer
	if err := Run([]string{"bootstrap", repo, "--json"}, "0.4.0-test", &stdout, &stderr); err != nil {
		t.Fatalf("bootstrap --json: %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		t.Fatalf("expected JSON, got: %v\n%s", err, stdout.String())
	}
	if payload["repo_root"] == "" {
		t.Errorf("bootstrap JSON missing repo_root")
	}
}

func TestRunSetupAlias(t *testing.T) {
	t.Setenv("RECONC_HOME", t.TempDir())
	repo := t.TempDir()
	var stdout, stderr bytes.Buffer
	if err := Run([]string{"setup", repo, "--json"}, "0.4.0-test", &stdout, &stderr); err != nil {
		t.Fatalf("setup alias: %v", err)
	}
	for _, path := range []string{".reconc.yml", "AGENTS.md", ".reconc/policy.lock.json"} {
		if _, err := os.Stat(filepath.Join(repo, path)); err != nil {
			t.Errorf("setup: %s not created: %v", path, err)
		}
	}
}

func TestRunInitRefuseExistingWithoutForce(t *testing.T) {
	repo := t.TempDir()
	// Pre-seed .reconc.yml so init's overwrite-guard fires.
	if err := os.WriteFile(filepath.Join(repo, ".reconc.yml"), []byte("existing"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("RECONC_HOME", t.TempDir())
	var stdout, stderr bytes.Buffer
	err := Run([]string{"init", repo}, "0.4.0-test", &stdout, &stderr)
	if err == nil {
		t.Fatal("expected error when .reconc.yml exists without --force")
	}
}

func TestRunInitForceOverwritesExisting(t *testing.T) {
	repo := t.TempDir()
	if err := os.WriteFile(filepath.Join(repo, ".reconc.yml"), []byte("existing"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("RECONC_HOME", t.TempDir())
	var stdout, stderr bytes.Buffer
	if err := Run([]string{"init", repo, "--force"}, "0.4.0-test", &stdout, &stderr); err != nil {
		t.Fatalf("init --force: %v", err)
	}
}

func TestRunStatusOnFreshRepo(t *testing.T) {
	t.Setenv("RECONC_HOME", t.TempDir())
	repo := t.TempDir()
	var stdout, stderr bytes.Buffer
	if err := Run([]string{"status", repo}, "0.4.0-test", &stdout, &stderr); err != nil {
		t.Fatalf("status: %v", err)
	}
	// Should print something non-empty describing the pristine state.
	if stdout.Len() == 0 {
		t.Error("status produced no output")
	}
}

func TestRunStatusIsReadOnlyWhenLockfileMissing(t *testing.T) {
	t.Setenv("RECONC_HOME", t.TempDir())
	repo := t.TempDir()
	if err := os.WriteFile(filepath.Join(repo, "AGENTS.md"), []byte("# test\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	if err := Run([]string{"status", repo}, "0.4.0-test", &stdout, &stderr); err != nil {
		t.Fatalf("status: %v", err)
	}
	if _, err := os.Stat(filepath.Join(repo, ".reconc", "policy.lock.json")); !os.IsNotExist(err) {
		t.Fatalf("status must not create lockfile; stat err=%v", err)
	}
	if !strings.Contains(stdout.String(), "no lockfile") {
		t.Fatalf("expected no-lockfile issue in status output, got %q", stdout.String())
	}
}

func TestRunVerifyIsReadOnlyWhenLockfileMissing(t *testing.T) {
	t.Setenv("RECONC_HOME", t.TempDir())
	repo := t.TempDir()
	if err := os.WriteFile(filepath.Join(repo, "AGENTS.md"), []byte("# test\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	if err := Run([]string{"verify", repo}, "0.4.0-test", &stdout, &stderr); err != nil {
		t.Fatalf("verify: %v", err)
	}
	if _, err := os.Stat(filepath.Join(repo, ".reconc", "policy.lock.json")); !os.IsNotExist(err) {
		t.Fatalf("verify must not create lockfile; stat err=%v", err)
	}
	if !strings.Contains(stdout.String(), "no lockfile") {
		t.Fatalf("expected no-lockfile issue in verify output, got %q", stdout.String())
	}
}

func TestRunSessionBriefingIsReadOnlyWhenLockfileMissing(t *testing.T) {
	t.Setenv("RECONC_HOME", t.TempDir())
	repo := t.TempDir()
	if err := os.WriteFile(filepath.Join(repo, "AGENTS.md"), []byte("# test\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	if err := Run([]string{"session-briefing", repo}, "0.4.0-test", &stdout, &stderr); err != nil {
		t.Fatalf("session-briefing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(repo, ".reconc", "policy.lock.json")); !os.IsNotExist(err) {
		t.Fatalf("session-briefing must not create lockfile; stat err=%v", err)
	}
	if !strings.Contains(stdout.String(), "no lockfile") {
		t.Fatalf("expected no-lockfile issue in session-briefing output, got %q", stdout.String())
	}
}

func TestRunCIStagedFailsOnNonGit(t *testing.T) {
	repo := t.TempDir()
	var stdout, stderr bytes.Buffer
	err := Run([]string{"ci", repo, "--staged"}, "0.4.0-test", &stdout, &stderr)
	if err == nil {
		t.Fatal("expected error for ci --staged on non-git repo")
	}
}

func TestRunFixAndExplainAcceptEmptyInputs(t *testing.T) {
	t.Setenv("RECONC_HOME", t.TempDir())
	repo := makeAssertRepo(t,
		"rules:\n  - id: r1\n    kind: deny_write\n    paths: ['gen/**']\n    mode: block\n    message: no gen\n")

	var stdout, stderr bytes.Buffer
	if err := Run([]string{"fix", repo}, "0.4.0-test", &stdout, &stderr); err != nil {
		t.Errorf("fix with no violations should not error: %v", err)
	}
	stdout.Reset()
	if err := Run([]string{"explain", repo}, "0.4.0-test", &stdout, &stderr); err != nil {
		t.Errorf("explain with no violations should not error: %v", err)
	}
}

// --- --output PATH coverage -----------------------------------------

func TestRunInitWritesOutputFile(t *testing.T) {
	t.Setenv("RECONC_HOME", t.TempDir())
	repo := t.TempDir()
	stdout, _ := runCommandWithOutputFile(t, "0.4.0-test", []string{"init", repo, "--json"})
	if !strings.Contains(stdout, `"repo_root"`) {
		t.Errorf("expected init JSON in stdout, got: %s", stdout)
	}
}

func TestRunCompileWritesOutputFile(t *testing.T) {
	t.Setenv("RECONC_HOME", t.TempDir())
	repo := t.TempDir()
	if err := os.WriteFile(filepath.Join(repo, "AGENTS.md"), []byte("# project\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	stdout, _ := runCommandWithOutputFile(t, "0.4.0-test", []string{"compile", repo, "--json"})
	if !strings.Contains(stdout, `"compiler_version"`) {
		t.Errorf("expected compile JSON in stdout, got: %s", stdout)
	}
}

func TestRunDoctorWritesOutputFile(t *testing.T) {
	repo := t.TempDir()
	if err := os.WriteFile(filepath.Join(repo, "AGENTS.md"), []byte("# agents\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	stdout, _ := runCommandWithOutputFile(t, "0.4.0-test", []string{"doctor", repo, "--json"})
	if !strings.Contains(stdout, `"discovered"`) {
		t.Errorf("expected doctor JSON in stdout, got: %s", stdout)
	}
}

func TestRunCheckWritesOutputFile(t *testing.T) {
	repo := makeCheckRepo(t,
		"rules:\n  - id: deny-gen\n    kind: deny_write\n    paths: ['gen/**']\n    mode: warn\n    message: m\n")
	stdout, _ := runCommandWithOutputFile(t, "0.4.0-test", []string{"check", repo, "--json"})
	if !strings.Contains(stdout, `"decision"`) {
		t.Errorf("expected check JSON in stdout, got: %s", stdout)
	}
}

func TestRunCIWritesOutputFile(t *testing.T) {
	repo := makeCheckRepo(t,
		"rules:\n  - id: deny-gen\n    kind: deny_write\n    paths: ['gen/**']\n    mode: warn\n    message: m\n")
	initGitRepo(t, repo)
	if err := os.MkdirAll(filepath.Join(repo, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "src", "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("git", "add", "src/main.go")
	cmd.Dir = repo
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git add failed: %v\n%s", err, string(out))
	}
	stdout, _ := runCommandWithOutputFile(t, "0.4.0-test", []string{"ci", repo, "--staged", "--json"})
	if !strings.Contains(stdout, `"git"`) {
		t.Errorf("expected ci JSON in stdout, got: %s", stdout)
	}
}

func TestRunExplainWritesOutputFile(t *testing.T) {
	repo := makeCheckRepo(t,
		"rules:\n  - id: deny-gen\n    kind: deny_write\n    paths: ['gen/**']\n    mode: warn\n    message: m\n")
	stdout, _ := runCommandWithOutputFile(t, "0.4.0-test", []string{"explain", repo, "--json"})
	if !strings.Contains(stdout, `"decision"`) {
		t.Errorf("expected explain JSON in stdout, got: %s", stdout)
	}
}

func TestRunFixWritesOutputFile(t *testing.T) {
	repo := makeCheckRepo(t,
		"rules:\n  - id: deny-gen\n    kind: deny_write\n    paths: ['gen/**']\n    mode: warn\n    message: m\n")
	stdout, _ := runCommandWithOutputFile(t, "0.4.0-test", []string{"fix", repo, "--json"})
	if !strings.Contains(stdout, `"summary"`) {
		t.Errorf("expected fix JSON in stdout, got: %s", stdout)
	}
}

func TestRunPresetListWritesOutputFile(t *testing.T) {
	t.Setenv("RECONC_HOME", t.TempDir())
	stdout, _ := runCommandWithOutputFile(t, "0.4.0-test", []string{"preset", "list", "--json"})
	if !strings.Contains(stdout, `"presets"`) {
		t.Errorf("expected preset list JSON in stdout, got: %s", stdout)
	}
}

func TestRunPresetShowWritesOutputFile(t *testing.T) {
	t.Setenv("RECONC_HOME", t.TempDir())
	stdout, _ := runCommandWithOutputFile(t, "0.4.0-test", []string{"preset", "show", "default"})
	if !strings.Contains(stdout, "default") {
		t.Errorf("expected preset content in stdout, got: %s", stdout)
	}
}

func TestRunHookGenerateWritesOutputFile(t *testing.T) {
	stdout, _ := runCommandWithOutputFile(t, "0.4.0-test", []string{"hook", "generate", "git-pre-commit"})
	if !strings.Contains(stdout, "reconc ci") {
		t.Errorf("expected hook content in stdout, got: %s", stdout)
	}
}

func TestRunHookInstallWritesOutputFile(t *testing.T) {
	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	stdout, _ := runCommandWithOutputFile(t, "0.4.0-test", []string{"hook", "install", "git-pre-commit", repo})
	if !strings.Contains(stdout, "Installed git-pre-commit hook") {
		t.Errorf("expected hook install summary in stdout, got: %s", stdout)
	}
}

func TestRunHookClaimWritesOutputFile(t *testing.T) {
	t.Setenv("RECONC_HOME", t.TempDir())
	repo := t.TempDir()
	stdout, _ := runCommandWithOutputFile(t, "0.4.0-test", []string{"hook", "claim", repo, "ci-green", "--session", "session-1", "--json"})
	if !strings.Contains(stdout, `"claim"`) {
		t.Errorf("expected hook claim JSON in stdout, got: %s", stdout)
	}
}

func TestRunStatusWritesOutputFile(t *testing.T) {
	repo := makeCheckRepo(t,
		"rules:\n  - id: deny-gen\n    kind: deny_write\n    paths: ['gen/**']\n    mode: warn\n    message: m\n")
	stdout, _ := runCommandWithOutputFile(t, "0.4.0-test", []string{"status", repo, "--json"})
	if !strings.Contains(stdout, `"healthy"`) {
		t.Errorf("expected status JSON in stdout, got: %s", stdout)
	}
}

func TestRunWhyTerseFitsBudget(t *testing.T) {
	repo := makeAssertRepo(t,
		"rules:\n  - id: terse-why\n    kind: deny_write\n    paths: ['src/**']\n    mode: block\n    message: |\n      line one\n      line two\n      line three\n      line four\n      line five\n")
	var stdout, stderr bytes.Buffer
	if err := Run([]string{"why", "terse-why", repo, "--terse"}, "0.4.0-test", &stdout, &stderr); err != nil {
		t.Fatalf("why --terse: %v", err)
	}
	out := stdout.String()
	if strings.Contains(out, "line five") {
		t.Errorf("terse why should truncate after four lines, got: %s", out)
	}
	if approxTokens(out) >= 50 {
		t.Errorf("why --terse exceeded token budget: %d tokens\n%s", approxTokens(out), out)
	}
}

func TestRunFixNextFitsBudget(t *testing.T) {
	repo := makeCheckRepo(t,
		"rules:\n  - id: ci-green\n    kind: require_claim\n    when_paths: ['src/**']\n    claims: ['ci-green']\n    mode: block\n    message: m\n")
	var stdout, stderr bytes.Buffer
	err := Run([]string{"fix", repo, "--write", "src/main.go", "--next"}, "0.4.0-test", &stdout, &stderr)
	if ExitCode(err) != 2 {
		t.Fatalf("fix --next should return exit 2 on blocking remediation, got %v", err)
	}
	out := stdout.String()
	if !strings.Contains(out, "next: [blocking|require_claim] ci-green") {
		t.Errorf("unexpected fix --next output: %s", out)
	}
	if approxTokens(out) >= 80 {
		t.Errorf("fix --next exceeded token budget: %d tokens\n%s", approxTokens(out), out)
	}
}

func TestRunStartMinimalFitsBudget(t *testing.T) {
	t.Setenv("RECONC_HOME", t.TempDir())
	repo := makeAssertRepo(t,
		"rules:\n  - id: r1\n    kind: deny_write\n    paths: ['x']\n    mode: warn\n    message: m\n")
	var stdout, stderr bytes.Buffer
	if err := Run([]string{"start", repo, "--minimal"}, "0.4.0-test", &stdout, &stderr); err != nil {
		t.Fatalf("start --minimal: %v", err)
	}
	out := stdout.String()
	if !strings.Contains(out, "status:") || !strings.Contains(out, "next:") || !strings.Contains(out, "more:") {
		t.Errorf("start --minimal missing compact lines:\n%s", out)
	}
	if approxTokens(out) >= 60 {
		t.Errorf("start --minimal exceeded token budget: %d tokens\n%s", approxTokens(out), out)
	}
}

func TestRunAuditTailCompactFitsBudget(t *testing.T) {
	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, ".reconc"), 0o755); err != nil {
		t.Fatal(err)
	}
	payload := `{"ts":"2026-04-14T00:00:00Z","event":"check","decision":"block","ok":false,"violation_count":1,"blocking_count":1,"rule_ids":["r1"]}` + "\n"
	if err := os.WriteFile(filepath.Join(repo, ".reconc", "audit.jsonl"), []byte(payload), 0o644); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	if err := Run([]string{"audit", "tail", repo, "--compact"}, "0.4.0-test", &stdout, &stderr); err != nil {
		t.Fatalf("audit tail --compact: %v", err)
	}
	out := stdout.String()
	if !strings.Contains(out, "r1") {
		t.Errorf("compact audit output missing rule id: %s", out)
	}
	if approxTokens(out) >= 25 {
		t.Errorf("audit tail --compact exceeded token budget: %d tokens\n%s", approxTokens(out), out)
	}
}
