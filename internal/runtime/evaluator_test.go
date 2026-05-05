package runtime

import (
	stderrors "errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"reconc.dev/reconc/internal/compiler"
	rerrors "reconc.dev/reconc/internal/errors"
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

func TestCheckRejectsMissingLockfile(t *testing.T) {
	withRECONCHome(t)
	repo := t.TempDir()
	writeFile(t, repo, "AGENTS.md", "# project\n")
	// note: no compile call

	_, err := CheckRepoPolicy(repo, Empty())
	if err == nil {
		t.Fatal("expected error for missing lockfile")
	}
}

func TestCheckRejectsStaleLockfile(t *testing.T) {
	withRECONCHome(t)
	repo := makeRepo(t, "# project\n", "", "rules:\n  - id: r\n    kind: deny_write\n    paths: ['x']\n    mode: warn\n    message: x\n")

	// Modify a source file AFTER compile -> source digest no longer matches.
	writeFile(t, repo, "policies/rules.yml",
		"rules:\n  - id: r\n    kind: deny_write\n    paths: ['x']\n    mode: warn\n    message: changed\n")

	_, err := CheckRepoPolicy(repo, Empty())
	if err == nil {
		t.Fatal("expected LockfileError for stale lockfile")
	}
	var lf *rerrors.LockfileError
	if !stderrors.As(err, &lf) {
		t.Errorf("expected *LockfileError, got %T: %v", err, err)
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
