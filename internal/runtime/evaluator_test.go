package runtime

import (
	stderrors "errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"reconc.dev/reconc/internal/compiler"
	rerrors "reconc.dev/reconc/internal/errors"
	"reconc.dev/reconc/internal/ingest"
	"reconc.dev/reconc/internal/policy"
)

// withRECONCHome isolates RECONC_HOME for tests.
func withRECONCHome(t *testing.T) {
	t.Helper()
	t.Setenv("RECONC_HOME", t.TempDir())
}

func writeFile(t *testing.T, dir, name, content string) {
	t.Helper()
	full := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", full, err)
	}
}

func TestSameCanonicalPathAcceptsCaseVariantSameFile(t *testing.T) {
	dir := t.TempDir()
	alias := strings.ToUpper(dir)
	if alias == dir {
		t.Skip("path has no case-variant alias")
	}
	dirInfo, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("stat dir: %v", err)
	}
	aliasInfo, err := os.Stat(alias)
	if err != nil || !os.SameFile(dirInfo, aliasInfo) {
		t.Skip("filesystem does not expose case-variant paths as the same directory")
	}
	if !sameCanonicalPath(dir, alias) {
		t.Fatalf("expected sameCanonicalPath to accept same-file case alias: %q vs %q", dir, alias)
	}
}

// makeRepo creates a minimal repo + compiles it. Returns the same
// path that was passed to compile so check() sees identical input
// (avoids macOS /var -> /private/var symlink mismatch).
func makeRepo(t *testing.T, agentsContent, configContent, policiesContent string) string {
	t.Helper()
	repo := t.TempDir()
	writeFile(t, repo, "AGENTS.md", agentsContent)
	if configContent != "" {
		writeFile(t, repo, ".reconc.yml", configContent)
	}
	if policiesContent != "" {
		writeFile(t, repo, "policies/rules.yml", policiesContent)
	}
	if _, err := compiler.CompileRepoPolicy(repo, "0.1.0-test"); err != nil {
		t.Fatalf("compile: %v", err)
	}
	return repo
}

// makeBundleRepoForCheck returns a repo where the policy is already
// compiled; tests pass writes/reads/commands/claims via ExecutionInputs.
func TestCheckPassesWhenNoEvidenceTriggers(t *testing.T) {
	withRECONCHome(t)
	repo := makeRepo(t, "# project\n", "", "rules:\n  - id: deny-gen\n    kind: deny_write\n    paths: ['gen/**']\n    mode: block\n    message: m\n")

	report, err := CheckRepoPolicy(repo, Empty())
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if !report.OK || report.Decision != DecisionPass {
		t.Errorf("expected pass, got %s (ok=%v)", report.Decision, report.OK)
	}
}

func TestCheckBlocksOnDenyWrite(t *testing.T) {
	withRECONCHome(t)
	repo := makeRepo(t, "# project\n", "", "rules:\n  - id: deny-gen\n    kind: deny_write\n    paths: ['gen/**']\n    mode: block\n    message: no gen edits\n")

	inputs := Empty()
	inputs.WritePaths = []string{"gen/output.go"}

	report, err := CheckRepoPolicy(repo, inputs)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if report.Decision != DecisionBlock {
		t.Errorf("expected block, got %s", report.Decision)
	}
	if report.BlockingViolationCount != 1 {
		t.Errorf("expected 1 blocking violation, got %d", report.BlockingViolationCount)
	}
}

func TestCheckPassesWhenDenyWriteScopeDoesntMatch(t *testing.T) {
	withRECONCHome(t)
	repo := makeRepo(t, "# project\n", "", "rules:\n  - id: deny-gen\n    kind: deny_write\n    paths: ['gen/**']\n    mode: block\n    message: m\n")

	inputs := Empty()
	inputs.WritePaths = []string{"src/main.go"}

	report, err := CheckRepoPolicy(repo, inputs)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if report.Decision != DecisionPass {
		t.Errorf("expected pass, got %s", report.Decision)
	}
}

func TestCheckBatchesWorkflowAuditRequireScripts(t *testing.T) {
	withRECONCHome(t)
	repo := t.TempDir()
	counterPath := filepath.Join(repo, "tools", "reconc", "harness", "demo", "audits", "counter")
	writeFile(t, repo, "AGENTS.md", "# project\n")
	script := strings.ReplaceAll(`#!/bin/sh
set -eu
printf x >> "__COUNTER__"
if [ "${1:-}" = "--batch-json" ]; then
  printf '{"results":[{"mode":"mode-a","failures":[]},{"mode":"mode-b","failures":["mode-b failed"]}]}\n'
  exit 2
fi
case "${1:-}" in
  mode-a) exit 0 ;;
  mode-b) printf 'mode-b failed\n'; exit 2 ;;
  *) printf 'unknown mode %s\n' "${1:-}"; exit 2 ;;
esac
`, "__COUNTER__", counterPath)
	writeFile(t, repo, "tools/reconc/harness/demo/audits/run-workflow-audit", script)
	if err := os.Chmod(filepath.Join(repo, "tools/reconc/harness/demo/audits/run-workflow-audit"), 0o755); err != nil {
		t.Fatalf("chmod script: %v", err)
	}
	writeFile(t, repo, "policies/rules.yml", `rules:
  - id: script-a
    kind: require_script
    when_paths: ['src/**']
    script: tools/reconc/harness/demo/audits/run-workflow-audit
    args: ['mode-a']
    mode: block
    message: mode a
  - id: script-b
    kind: require_script
    when_paths: ['src/**']
    script: tools/reconc/harness/demo/audits/run-workflow-audit
    args: ['mode-b']
    mode: block
    message: mode b
`)
	if _, err := compiler.CompileRepoPolicy(repo, "0.1.0-test"); err != nil {
		t.Fatalf("compile: %v", err)
	}

	inputs := Empty()
	inputs.WritePaths = []string{"src/main.go"}
	report, err := CheckRepoPolicy(repo, inputs)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if report.Decision != DecisionBlock {
		t.Fatalf("expected block, got %s", report.Decision)
	}
	if len(report.Violations) != 1 || report.Violations[0].RuleID != "script-b" {
		t.Fatalf("expected only script-b violation, got %#v", report.Violations)
	}
	counter, err := os.ReadFile(counterPath)
	if err != nil {
		t.Fatalf("read counter: %v", err)
	}
	if string(counter) != "x" {
		t.Fatalf("expected one batched script invocation, got %q", string(counter))
	}
}

func TestCheckRequireRead(t *testing.T) {
	withRECONCHome(t)
	repo := makeRepo(t, "# project\n", "",
		"rules:\n  - id: arch-read\n    kind: require_read\n    paths: ['src/**']\n    before_paths: ['ARCHITECTURE.md']\n    mode: block\n    message: read first\n")

	// Without read: should block.
	inputs := Empty()
	inputs.WritePaths = []string{"src/main.go"}
	report, _ := CheckRepoPolicy(repo, inputs)
	if report.Decision != DecisionBlock {
		t.Errorf("expected block when read missing, got %s", report.Decision)
	}

	// With read: should pass.
	inputs.ReadPaths = []string{"ARCHITECTURE.md"}
	report, _ = CheckRepoPolicy(repo, inputs)
	if report.Decision != DecisionPass {
		t.Errorf("expected pass with read present, got %s", report.Decision)
	}
}

func TestCheckCoupleChange(t *testing.T) {
	withRECONCHome(t)
	repo := makeRepo(t, "# project\n", "",
		"rules:\n  - id: tests-follow\n    kind: couple_change\n    paths: ['src/**']\n    when_paths: ['tests/**']\n    mode: block\n    message: tests required\n")

	// Source change without test - block.
	inputs := Empty()
	inputs.WritePaths = []string{"src/main.go"}
	report, _ := CheckRepoPolicy(repo, inputs)
	if report.Decision != DecisionBlock {
		t.Errorf("expected block, got %s", report.Decision)
	}

	// Source + test - pass.
	inputs.WritePaths = []string{"src/main.go", "tests/main_test.go"}
	report, _ = CheckRepoPolicy(repo, inputs)
	if report.Decision != DecisionPass {
		t.Errorf("expected pass, got %s", report.Decision)
	}
}

func TestCheckRequireCommand(t *testing.T) {
	withRECONCHome(t)
	repo := makeRepo(t, "# project\n", "",
		"rules:\n  - id: must-test\n    kind: require_command\n    when_paths: ['src/**']\n    commands: ['go test']\n    mode: warn\n    message: run tests\n")

	inputs := Empty()
	inputs.WritePaths = []string{"src/main.go"}
	report, _ := CheckRepoPolicy(repo, inputs)
	if report.Decision != DecisionWarn {
		t.Errorf("expected warn, got %s", report.Decision)
	}

	inputs.Commands = []string{"go test"}
	report, _ = CheckRepoPolicy(repo, inputs)
	if report.Decision != DecisionPass {
		t.Errorf("expected pass with command run, got %s", report.Decision)
	}
}

func TestCheckRequireCommandSuccess(t *testing.T) {
	withRECONCHome(t)
	repo := makeRepo(t, "# project\n", "",
		"rules:\n  - id: tests-must-pass\n    kind: require_command_success\n    when_paths: ['src/**']\n    commands: ['go test']\n    mode: block\n    message: tests must pass\n")

	inputs := Empty()
	inputs.WritePaths = []string{"src/main.go"}
	inputs.Commands = []string{"go test"}
	// Command was run but not marked as success
	report, _ := CheckRepoPolicy(repo, inputs)
	if report.Decision != DecisionBlock {
		t.Errorf("expected block when command not marked success, got %s", report.Decision)
	}

	// Now mark as success
	inputs.CommandResults = []CommandResult{{Command: "go test", Outcome: CommandOutcomeSuccess}}
	report, _ = CheckRepoPolicy(repo, inputs)
	if report.Decision != DecisionPass {
		t.Errorf("expected pass on success outcome, got %s", report.Decision)
	}

	// Failure should also block
	inputs.CommandResults = []CommandResult{{Command: "go test", Outcome: CommandOutcomeFailure}}
	report, _ = CheckRepoPolicy(repo, inputs)
	if report.Decision != DecisionBlock {
		t.Errorf("expected block on failure outcome, got %s", report.Decision)
	}
}

func TestCheckForbidCommand(t *testing.T) {
	withRECONCHome(t)
	repo := makeRepo(t, "# project\n", "",
		"rules:\n  - id: no-rm-rf\n    kind: forbid_command\n    commands: ['rm -rf /']\n    mode: block\n    message: never\n")

	inputs := Empty()
	inputs.Commands = []string{"rm -rf /"}
	report, _ := CheckRepoPolicy(repo, inputs)
	if report.Decision != DecisionBlock {
		t.Errorf("expected block, got %s", report.Decision)
	}

	inputs.Commands = []string{"ls"}
	report, _ = CheckRepoPolicy(repo, inputs)
	if report.Decision != DecisionPass {
		t.Errorf("expected pass when forbidden cmd not run, got %s", report.Decision)
	}
}

func TestCheckForbidCommandWithScope(t *testing.T) {
	withRECONCHome(t)
	repo := makeRepo(t, "# project\n", "",
		"rules:\n  - id: no-pip-on-pyproject\n    kind: forbid_command\n    when_paths: ['pyproject.toml']\n    commands: ['pip install']\n    mode: block\n    message: use uv\n")

	// Forbidden command but scope not touched -> pass.
	inputs := Empty()
	inputs.Commands = []string{"pip install"}
	report, _ := CheckRepoPolicy(repo, inputs)
	if report.Decision != DecisionPass {
		t.Errorf("expected pass when scope not touched, got %s", report.Decision)
	}

	// Both: scope touched + forbidden command -> block.
	inputs.WritePaths = []string{"pyproject.toml"}
	report, _ = CheckRepoPolicy(repo, inputs)
	if report.Decision != DecisionBlock {
		t.Errorf("expected block when scope+command match, got %s", report.Decision)
	}
}

func TestCheckRequireClaim(t *testing.T) {
	withRECONCHome(t)
	repo := makeRepo(t, "# project\n", "",
		"rules:\n  - id: ci-green\n    kind: require_claim\n    when_paths: ['src/**']\n    claims: ['ci-green']\n    mode: block\n    message: need ci\n")

	inputs := Empty()
	inputs.WritePaths = []string{"src/main.go"}
	report, _ := CheckRepoPolicy(repo, inputs)
	if report.Decision != DecisionBlock {
		t.Errorf("expected block without claim, got %s", report.Decision)
	}

	inputs.Claims = []string{"ci-green"}
	report, _ = CheckRepoPolicy(repo, inputs)
	if report.Decision != DecisionPass {
		t.Errorf("expected pass with claim, got %s", report.Decision)
	}
}

func TestCheckRejectsPathOutsideRepo(t *testing.T) {
	withRECONCHome(t)
	repo := makeRepo(t, "# project\n", "", "rules:\n  - id: x\n    kind: deny_write\n    paths: ['x/**']\n    mode: warn\n    message: x\n")

	inputs := Empty()
	inputs.WritePaths = []string{"/etc/passwd"}

	_, err := CheckRepoPolicy(repo, inputs)
	if err == nil {
		t.Fatal("expected RepoBoundaryError")
	}
	var rb *rerrors.RepoBoundaryError
	if !stderrors.As(err, &rb) {
		t.Errorf("expected *RepoBoundaryError, got %T: %v", err, err)
	}
}

func TestCheckRequiresExplicitRefreshForMissingLockfile(t *testing.T) {
	withRECONCHome(t)
	repo := t.TempDir()
	writeFile(t, repo, "AGENTS.md", "# project\n")

	_, err := CheckRepoPolicy(repo, Empty())
	if err == nil || !strings.Contains(err.Error(), "reconc refresh .") {
		t.Fatalf("expected explicit refresh error, got %v", err)
	}
	if strings.Count(err.Error(), "reconc refresh .") != 1 {
		t.Fatalf("refresh remediation must appear exactly once, got %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(repo, ingest.LockfilePath)); !os.IsNotExist(statErr) {
		t.Fatalf("check must not create a lockfile, stat err=%v", statErr)
	}
}

func TestCheckRequiresExplicitRefreshForStaleLockfile(t *testing.T) {
	withRECONCHome(t)
	repo := makeRepo(t, "# project\n", "", "rules:\n  - id: r\n    kind: deny_write\n    paths: ['x']\n    mode: warn\n    message: x\n")
	lockPath := filepath.Join(repo, ingest.LockfilePath)
	before, err := os.ReadFile(lockPath)
	if err != nil {
		t.Fatal(err)
	}

	// Modify a source file AFTER compile -> source digest no longer matches.
	writeFile(t, repo, "policies/rules.yml",
		"rules:\n  - id: r\n    kind: deny_write\n    paths: ['x']\n    mode: warn\n    message: changed\n")

	_, err = CheckRepoPolicy(repo, Empty())
	if err == nil || !strings.Contains(err.Error(), "reconc refresh .") {
		t.Fatalf("expected explicit refresh error, got %v", err)
	}
	after, readErr := os.ReadFile(lockPath)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(after) != string(before) {
		t.Fatal("check modified the stale lockfile")
	}
}

func TestCheckPathsNormalizedToRepoRelative(t *testing.T) {
	withRECONCHome(t)
	repo := makeRepo(t, "# project\n", "", "rules:\n  - id: deny-gen\n    kind: deny_write\n    paths: ['gen/**']\n    mode: block\n    message: x\n")

	// Pass an absolute path inside the repo.
	inputs := Empty()
	inputs.WritePaths = []string{filepath.Join(repo, "gen", "output.go")}

	report, err := CheckRepoPolicy(repo, inputs)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if report.Decision != DecisionBlock {
		t.Errorf("expected block on absolute-path-inside-repo, got %s", report.Decision)
	}
	if report.Inputs.WritePaths[0] != "gen/output.go" {
		t.Errorf("expected normalized path 'gen/output.go', got %q", report.Inputs.WritePaths[0])
	}
}

func TestCheckBackslashPathsNormalizedToPOSIX(t *testing.T) {
	withRECONCHome(t)
	repo := makeRepo(t, "# project\n", "", "rules:\n  - id: deny-gen\n    kind: deny_write\n    paths: ['gen/**']\n    mode: block\n    message: x\n")

	inputs := Empty()
	inputs.WritePaths = []string{`gen\output.go`}

	report, err := CheckRepoPolicy(repo, inputs)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if report.Decision != DecisionBlock {
		t.Errorf("expected block on backslash path, got %s", report.Decision)
	}
	if report.Inputs.WritePaths[0] != "gen/output.go" {
		t.Errorf("expected normalized path 'gen/output.go', got %q", report.Inputs.WritePaths[0])
	}
}

func TestCheckSummariesAndCounts(t *testing.T) {
	withRECONCHome(t)
	repo := makeRepo(t, "# project\n", "",
		"rules:\n  - id: r1\n    kind: deny_write\n    paths: ['gen/**']\n    mode: block\n    message: m1\n  - id: r2\n    kind: deny_write\n    paths: ['dist/**']\n    mode: warn\n    message: m2\n")

	inputs := Empty()
	inputs.WritePaths = []string{"gen/a.go", "dist/b.go"}

	report, _ := CheckRepoPolicy(repo, inputs)
	if report.ViolationCount != 2 {
		t.Errorf("expected 2 violations, got %d", report.ViolationCount)
	}
	if report.BlockingViolationCount != 1 {
		t.Errorf("expected 1 blocking, got %d", report.BlockingViolationCount)
	}
	if report.Decision != DecisionBlock {
		t.Errorf("expected block decision, got %s", report.Decision)
	}
	if report.Summary == "" {
		t.Error("summary should be non-empty")
	}
	if report.NextAction == "" {
		t.Error("next_action should be set when violations exist")
	}
}

// --- W17: scoped rules (monorepo) ------------------------------------

func TestCheckScopedRuleOnlyFiresInsideScope(t *testing.T) {
	withRECONCHome(t)
	policies := `default_mode: warn
scopes:
  - id: web
    paths: ['apps/web/**']
    rules:
      - id: web-gen
        kind: deny_write
        paths: ['apps/web/generated/**']
        mode: block
        message: web-generated is read-only
`
	repo := makeRepo(t, "# t\n", "", policies)

	// Write inside the web scope -> rule fires.
	report, err := CheckRepoPolicy(repo, ExecutionInputs{
		WritePaths: []string{filepath.Join(repo, "apps/web/generated/x.ts")},
	})
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if report.Decision != DecisionBlock {
		t.Errorf("scoped rule should fire inside scope; got decision %s", report.Decision)
	}

	// Write outside the web scope -> rule does NOT fire (rule scope
	// paths don't match, so the rule is filtered out before its own
	// paths matcher gets a chance).
	report2, err := CheckRepoPolicy(repo, ExecutionInputs{
		WritePaths: []string{filepath.Join(repo, "libs/shared/x.ts")},
	})
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if report2.Decision != DecisionPass {
		t.Errorf("scoped rule should NOT fire outside scope; got decision %s, violations=%v",
			report2.Decision, report2.Violations)
	}
}

func TestCheckGlobalAndScopedCoexist(t *testing.T) {
	withRECONCHome(t)
	policies := `default_mode: warn
rules:
  - id: no-secrets
    kind: deny_write
    paths: ['**/.env']
    mode: block
    message: no secrets
scopes:
  - id: web
    paths: ['apps/web/**']
    rules:
      - id: web-gen
        kind: deny_write
        paths: ['apps/web/generated/**']
        mode: block
        message: web-gen
`
	repo := makeRepo(t, "# t\n", "", policies)

	// Global rule always applies regardless of scope.
	report, err := CheckRepoPolicy(repo, ExecutionInputs{
		WritePaths: []string{filepath.Join(repo, "libs/shared/.env")},
	})
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if report.Decision != DecisionBlock {
		t.Errorf("global rule should fire anywhere; got %s", report.Decision)
	}
	if len(report.Violations) != 1 || report.Violations[0].RuleID != "no-secrets" {
		t.Errorf("expected only global rule to fire; got violations %+v", report.Violations)
	}
}

func TestCheckMultipleScopesIndependent(t *testing.T) {
	withRECONCHome(t)
	policies := `default_mode: warn
scopes:
  - id: web
    paths: ['apps/web/**']
    rules:
      - id: web-r
        kind: deny_write
        paths: ['apps/web/**']
        mode: block
        message: w
  - id: mobile
    paths: ['apps/mobile/**']
    rules:
      - id: mobile-r
        kind: deny_write
        paths: ['apps/mobile/**']
        mode: block
        message: m
`
	repo := makeRepo(t, "# t\n", "", policies)

	// Writing only in web should not trip mobile's rule.
	report, err := CheckRepoPolicy(repo, ExecutionInputs{
		WritePaths: []string{filepath.Join(repo, "apps/web/x")},
	})
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	for _, v := range report.Violations {
		if v.RuleID == "mobile-r" {
			t.Errorf("mobile rule should not fire for a web-scope write; got %+v", v)
		}
	}
}

// --- W24: custom schema URL backward compatibility ------------------

func TestCheckAcceptsDefaultSchemaWhenEnvOverrideSet(t *testing.T) {
	// Compile with no override -> lockfile has default schema URL.
	withRECONCHome(t)
	policies := "default_mode: warn\nrules:\n  - id: r\n    kind: deny_write\n    paths: ['x']\n    mode: warn\n    message: m\n"
	repo := makeRepo(t, "# t\n", "", policies)

	// Now flip the env and check -- reader must still accept the
	// default schema URL for back-compat.
	t.Setenv("RECONC_SCHEMA_BASE_URL", "https://acme.com")
	if _, err := CheckRepoPolicy(repo, ExecutionInputs{}); err != nil {
		t.Errorf("reader should accept default schema even when override is set; got: %v", err)
	}
}

// --- scope-filter fail-closed ---------------------------------------

func TestScopeFilterPatternErrorFailsClosed(t *testing.T) {
	// Craft a lockfile rule with a malformed scope_paths pattern.
	// doublestar rejects certain malformed glob character classes.
	withRECONCHome(t)
	policies := `default_mode: warn
scopes:
  - id: web
    paths: ['apps/web/**']
    rules:
      - id: scoped-rule
        kind: deny_write
        paths: ['apps/web/generated/**']
        mode: block
        message: m
`
	repo := makeRepo(t, "# t\n", "", policies)

	// Now inject a bad scope_paths into the compiled lockfile by
	// rewriting one entry. Crude but test-focused; a real malformed
	// scope would surface at compile time.
	lockfilePath := filepath.Join(repo, ".reconc", "policy.lock.json")
	data, err := os.ReadFile(lockfilePath)
	if err != nil {
		t.Fatal(err)
	}
	// Replace apps/web/** with a syntactically-invalid glob.
	corrupted := strings.ReplaceAll(string(data), `"apps/web/**"`, `"[malformed"`)
	if err := os.WriteFile(lockfilePath, []byte(corrupted), 0o644); err != nil {
		t.Fatal(err)
	}

	// Any write triggers the scope evaluation.
	report, err := CheckRepoPolicy(repo, ExecutionInputs{
		WritePaths: []string{filepath.Join(repo, "apps/web/src/x.go")},
	})
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	// Expect a synthetic scope-pattern-error violation in block mode.
	var found *Violation
	for i := range report.Violations {
		if strings.Contains(report.Violations[i].Message, "scope pattern") {
			found = &report.Violations[i]
		}
	}
	if found == nil {
		t.Fatalf("expected synthetic scope-pattern-error violation, got: %+v", report.Violations)
	}
	if found.Mode != policy.ModeBlock {
		t.Errorf("scope-pattern-error must be block mode, got %s", found.Mode)
	}
}

// --- interior whitespace collapse ----------------------------------

func TestCommandMatchCollapsesInteriorSpace(t *testing.T) {
	withRECONCHome(t)
	policies := `default_mode: warn
rules:
  - id: r1
    kind: require_command
    when_paths: ['src/**']
    commands: ['go test']
    mode: block
    message: m
`
	repo := makeRepo(t, "# t\n", "", policies)

	// Agent reports the command with a double space.
	report, err := CheckRepoPolicy(repo, ExecutionInputs{
		WritePaths: []string{filepath.Join(repo, "src/x.go")},
		Commands:   []string{"go  test"}, // double space
	})
	if err != nil {
		t.Fatal(err)
	}
	// Must NOT produce a block (whitespace-only difference must match).
	if report.Decision == DecisionBlock {
		t.Errorf("double-space command should match single-space policy, got block: %+v", report.Violations)
	}
}

func TestCommandMatchRejectsNonWhitespaceDifference(t *testing.T) {
	// Negative control: "go test" vs "gotest" (no space) must still
	// be different -- we're only collapsing whitespace, not ignoring
	// it.
	withRECONCHome(t)
	policies := `default_mode: warn
rules:
  - id: r1
    kind: require_command
    when_paths: ['src/**']
    commands: ['go test']
    mode: block
    message: m
`
	repo := makeRepo(t, "# t\n", "", policies)
	report, _ := CheckRepoPolicy(repo, ExecutionInputs{
		WritePaths: []string{filepath.Join(repo, "src/x.go")},
		Commands:   []string{"gotest"},
	})
	if report.Decision != DecisionBlock {
		t.Errorf("'gotest' should NOT match 'go test', got decision=%s", report.Decision)
	}
}

func TestClaimMatchCollapsesInteriorWhitespace(t *testing.T) {
	withRECONCHome(t)
	policies := `default_mode: warn
rules:
  - id: r1
    kind: require_claim
    when_paths: ['src/**']
    claims: ['ci-green']
    mode: block
    message: m
`
	repo := makeRepo(t, "# t\n", "", policies)
	// Claim reported with a trailing tab + spaces.
	report, _ := CheckRepoPolicy(repo, ExecutionInputs{
		WritePaths: []string{filepath.Join(repo, "src/x.go")},
		Claims:     []string{"ci-green\t"},
	})
	if report.Decision == DecisionBlock {
		t.Errorf("whitespace-padded claim should satisfy require_claim, got block")
	}
}

// --- path normalisation resolves symlinks ---------------------------

func TestNormalizeRejectsSymlinkEscapingRepo(t *testing.T) {
	withRECONCHome(t)
	// Pristine repo (no rules) -- we only care about the boundary
	// check which runs before any rule evaluator.
	repo := makeRepo(t, "# t\n", "", "rules: []\n")

	// Create a symlink inside the repo that points OUTSIDE.
	outside := t.TempDir()
	escape := filepath.Join(repo, "escape")
	if err := os.Symlink(outside, escape); err != nil {
		t.Skipf("symlink not supported: %v", err)
	}

	_, err := CheckRepoPolicy(repo, ExecutionInputs{
		WritePaths: []string{filepath.Join(repo, "escape/secret")},
	})
	if err == nil {
		t.Fatal("expected RepoBoundaryError for symlink-escape write")
	}
	var rbe *rerrors.RepoBoundaryError
	if !stderrors.As(err, &rbe) {
		t.Errorf("expected RepoBoundaryError, got %T: %v", err, err)
	}
}

// TestNormalizeCommandSemantics_RTKPrefix pins that "rtk " is stripped
// when it appears at the start of a command position (start of command
// or after a shell compound boundary), but not when it appears mid-token.
func TestNormalizeCommandSemantics_RTKPrefix(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"start_of_command", "rtk go test ./...", "go test ./..."},
		{"after_and", "cd tools && rtk go test ./...", "cd tools && go test ./..."},
		{"after_or", "false || rtk echo ok", "false || echo ok"},
		{"after_semicolon", "false ; rtk echo ok", "false ; echo ok"},
		{"after_pipe", "cat x | rtk grep y", "cat x | grep y"},
		{"after_background", "sleep 1 & rtk ls", "sleep 1 & ls"},
		{"multiple_rtks", "rtk a && rtk b && rtk c", "a && b && c"},
		{"no_rewrite_for_rtkfoo", "rtkfoo bar", "rtkfoo bar"},
		{"no_rewrite_when_no_trailing_space", "rtk", "rtk"},
		{"no_rewrite_when_rtk_inside_path", "ls /home/rtk/dir", "ls /home/rtk/dir"},
		{"no_rewrite_when_rtk_in_string_literal", "echo 'rtk inside'", "echo 'rtk inside'"},
		{"empty_input", "", ""},
		{"whitespace_only", "   \t  ", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := normalizeCommandSemantics(tt.in, "")
			if got != tt.want {
				t.Errorf("normalizeCommandSemantics(%q, \"\") = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

// TestNormalizeCommandSemantics_RepoRootCD pins that an absolute repo
// path inside `cd` is resolved to its relative form, but only at
// command-position segment boundaries.
func TestNormalizeCommandSemantics_RepoRootCD(t *testing.T) {
	const repo = "/workspace/project"
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"cd_into_repo_subdir", "cd " + repo + "/tools/reconc && go test", "cd tools/reconc && go test"},
		{"cd_into_repo_root", "cd " + repo, "cd ."},
		{"cd_into_repo_root_then_cmd", "cd " + repo + " && go build", "cd . && go build"},
		{"cd_into_different_dir_unchanged", "cd /tmp/other && go test", "cd /tmp/other && go test"},
		{"cd_into_similar_prefix_not_substring", "cd " + repo + "Backup && ls", "cd " + repo + "Backup && ls"},
		{"echo_path_unchanged", "echo " + repo + "/sub", "echo " + repo + "/sub"},
		{"mid_command_path_unchanged", "ls && echo " + repo, "ls && echo " + repo},
		{"trailing_slash_on_repoRoot_input", "cd " + repo + "/sub", "cd sub"},
		{"empty_repoRoot_skips_cd_normalization", "cd " + repo, "cd " + repo},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := repo
			if tt.name == "empty_repoRoot_skips_cd_normalization" {
				root = ""
			}
			got := normalizeCommandSemantics(tt.in, root)
			if got != tt.want {
				t.Errorf("normalizeCommandSemantics(%q, %q) = %q, want %q", tt.in, root, got, tt.want)
			}
		})
	}
}

// TestNormalizeCommandSemantics_Combined exercises the realistic
// production case where BOTH rewrites must apply: Claude Code anchors
// to absolute repo path AND RTK rewrites with `rtk` prefix.
func TestNormalizeCommandSemantics_Combined(t *testing.T) {
	const repo = "/workspace/project"
	in := "cd " + repo + "/tools/reconc/harness/project && rtk go test ./..."
	want := "cd tools/reconc/harness/project && go test ./..."
	got := normalizeCommandSemantics(in, repo)
	if got != want {
		t.Errorf("combined rewrite: got %q, want %q", got, want)
	}
}

// TestNormalizeCommandSemantics_Idempotent guarantees the transformation
// converges in one pass: repeated application produces the same string.
func TestNormalizeCommandSemantics_Idempotent(t *testing.T) {
	const repo = "/workspace/project"
	inputs := []string{
		"rtk go test ./...",
		"cd " + repo + "/tools && go build",
		"cd " + repo + "/x && rtk a && rtk b",
		"echo " + repo,
		"true",
	}
	for _, in := range inputs {
		once := normalizeCommandSemantics(in, repo)
		twice := normalizeCommandSemantics(once, repo)
		if once != twice {
			t.Errorf("not idempotent for %q: once=%q twice=%q", in, once, twice)
		}
	}
}

// TestNormalizeCommandSemantics_TrailingSlashRoot ensures a trailing
// slash on repoRoot does not break the prefix-strip.
func TestNormalizeCommandSemantics_TrailingSlashRoot(t *testing.T) {
	const repoWithSlash = "/workspace/project/"
	got := normalizeCommandSemantics("cd /workspace/project/sub && ls", repoWithSlash)
	want := "cd sub && ls"
	if got != want {
		t.Errorf("trailing-slash repoRoot: got %q, want %q", got, want)
	}
}

// TestMatchingCommandsForbidSafety pins the critical invariant that
// adding RTK/path normalisation does NOT broaden forbid semantics.
// A literal `rtk rm -rf /` matches `rm -rf /` after normalisation
// (intended — rtk-wrapping does not exempt from forbid). But a
// quoted/echoed substring like `echo 'rm -rf /'` must still NOT match
// the literal `rm -rf /` rule, because the segment after normalisation
// is `echo 'rm -rf /'`, a different string.
func TestMatchingCommandsForbidSafety(t *testing.T) {
	expected := []string{"rm -rf /"}
	cases := []struct {
		name   string
		actual []string
		want   []string
	}{
		{"literal_match", []string{"rm -rf /"}, []string{"rm -rf /"}},
		{"rtk_wrapped_match", []string{"rtk rm -rf /"}, []string{"rtk rm -rf /"}},
		{"echo_substring_no_match", []string{"echo 'rm -rf /'"}, nil},
		{"unrelated_no_match", []string{"ls -la"}, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := matchingCommands(tc.actual, expected, "")
			if len(got) != len(tc.want) {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Errorf("got[%d] = %q, want %q", i, got[i], tc.want[i])
				}
			}
		})
	}
}

// TestNormalizeCommandSemantics_CdArgEdgeCases pins three subtle path
// shapes that path.Clean must canonicalise so the cd-prefix-strip
// matches reliably: trailing slash, doubled slash, parent traversal.
// Discovered during reality check; without path.Clean these silently
// fell through and produced `cd` (bare), `cd /sub`, or `cd sub/..`.
func TestNormalizeCommandSemantics_CdArgEdgeCases(t *testing.T) {
	const repo = "/workspace/project"
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"trailing_slash_on_cd_arg", "cd " + repo + "/", "cd ."},
		{"dot_after_repo", "cd " + repo + "/.", "cd ."},
		{"double_slash_in_cd", "cd " + repo + "//sub", "cd sub"},
		{"parent_traversal_back_to_root", "cd " + repo + "/sub/..", "cd ."},
		{"parent_traversal_to_other_sub", "cd " + repo + "/sub/../other", "cd other"},
		{"dot_in_middle", "cd " + repo + "/sub/./inner", "cd sub/inner"},
		{"quoted_arg_left_alone", "cd \"" + repo + "/sub\"", "cd \"" + repo + "/sub\""},
		{"single_quoted_arg_left_alone", "cd '" + repo + "/sub'", "cd '" + repo + "/sub'"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := normalizeCommandSemantics(tt.in, repo)
			if got != tt.want {
				t.Errorf("normalizeCommandSemantics(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

// TestNormalizeCommandSemantics_RTKHookClaudeAware pins that `rtk` used
// not as a proxy but as the rtk-binary's own subcommand (e.g.
// `rtk hook claude` from the agent runtime configuration) still gets
// its prefix stripped — that is the literal semantics and Reconc
// rules do not target rtk's own subcommands, so this is safe in
// practice. Recorded here for awareness, not as a feature.
func TestNormalizeCommandSemantics_RTKHookClaudeAware(t *testing.T) {
	got := normalizeCommandSemantics("rtk hook claude", "")
	want := "hook claude"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// TestNormalizeCommandSemantics_QuoteSplitterLimitation documents the
// known limitation of the segment splitter: it does not understand
// shell quoting, so a literal `&&` that lives inside a quoted string
// AND is surrounded by spaces will be treated as a compound boundary.
// The transformation still applies symmetrically on both sides of the
// match, so a rule authored against the same quoted form continues to
// match correctly. Quote-wrapped commands without space around the
// boundary are unaffected because the literal separator is ` && `
// (single-space pads).
func TestNormalizeCommandSemantics_QuoteSplitterLimitation(t *testing.T) {
	t.Run("quotes_without_space_around_compound_preserved", func(t *testing.T) {
		got := normalizeCommandSemantics(`echo "&& rtk x"`, "")
		want := `echo "&& rtk x"`
		if got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	})
	t.Run("quotes_with_space_around_compound_naively_split", func(t *testing.T) {
		// Acknowledged limitation: splitter is naive, transformation
		// still symmetric so production rule-matching is unaffected.
		got := normalizeCommandSemantics(`echo "a && rtk b"`, "")
		want := `echo "a && b"`
		if got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	})
}

// TestMatchingCommandResultsAppliesNormalization pins the integration
// path that this whole change exists for: a recorded command with
// absolute repo path + rtk prefix matches the rule's literal form via
// matchingCommandResults.
func TestMatchingCommandResultsAppliesNormalization(t *testing.T) {
	const repo = "/workspace/project"
	results := []CommandResult{
		{Command: "cd " + repo + "/tools/reconc/harness/project && rtk go test ./...", Outcome: CommandOutcomeSuccess},
	}
	expected := []string{"cd tools/reconc/harness/project && go test ./..."}
	got := matchingCommandResults(results, expected, CommandOutcomeSuccess, repo)
	if len(got) != 1 {
		t.Fatalf("expected 1 match, got %d: %v", len(got), got)
	}
}

func TestMatchingCommandResultsToleratesTrailingRedirects(t *testing.T) {
	const repo = "/workspace/project"
	expected := []string{"cd tools/reconc && go test ./..."}
	// Redirections preserve the command's own exit status, so a recorded
	// success with a trailing redirect genuinely means the command succeeded.
	for _, recorded := range []string{
		"cd " + repo + "/tools/reconc && rtk go test ./... 2>&1",
		"cd tools/reconc && go test ./... > out.log",
		"cd tools/reconc && go test ./... >>build.log",
		"cd tools/reconc && go test ./... 2> err.log",
		"cd tools/reconc && go test ./... > /dev/null 2>&1",
	} {
		got := matchingCommandResults(
			[]CommandResult{{Command: recorded, Outcome: CommandOutcomeSuccess}},
			expected, CommandOutcomeSuccess, repo)
		if len(got) != 1 {
			t.Fatalf("expected redirect-tolerant match for %q, got %d: %v", recorded, len(got), got)
		}
	}
}

func TestMatchingCommandResultsRejectsPipeAndExtraArgs(t *testing.T) {
	const repo = "/workspace/project"
	expected := []string{"cd tools/reconc && go test ./..."}
	// Pipes mask the real exit status (pipeline status is the last stage's),
	// and extra args change what ran - neither may satisfy the rule.
	for _, recorded := range []string{
		"cd tools/reconc && go test ./... | tail -5",
		"cd tools/reconc && go test ./... 2>&1 | tail -5",
		"cd tools/reconc && go test ./... -run TestX",
		"cd tools/reconc && go test ./... && echo done",
	} {
		got := matchingCommandResults(
			[]CommandResult{{Command: recorded, Outcome: CommandOutcomeSuccess}},
			expected, CommandOutcomeSuccess, repo)
		if len(got) != 0 {
			t.Fatalf("expected NO match for %q (unsafe/changed semantics), got %v", recorded, got)
		}
	}
}

func TestStripTrailingRedirects(t *testing.T) {
	cases := []struct{ in, want string }{
		{"go test ./...", "go test ./..."},
		{"go test ./... 2>&1", "go test ./..."},
		{"go test ./... > out.log", "go test ./..."},
		{"go test ./... >>out.log", "go test ./..."},
		{"go test ./... 2> err", "go test ./..."},
		{"go test ./... > /dev/null 2>&1", "go test ./..."},
		{"go test ./... < input.txt", "go test ./..."},
		{"go test ./... | tail -5", "go test ./... | tail -5"},
		{"go test ./... 2>&1 | tail", "go test ./... 2>&1 | tail"},
		{"go test ./... -run X", "go test ./... -run X"},
		{"go run main.go", "go run main.go"},
	}
	for _, c := range cases {
		if got := stripTrailingRedirects(c.in); got != c.want {
			t.Errorf("stripTrailingRedirects(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
