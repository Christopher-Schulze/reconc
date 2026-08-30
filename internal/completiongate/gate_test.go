package completiongate_test

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"testing"
	"time"

	"reconc.dev/reconc/internal/commandproof"
	"reconc.dev/reconc/internal/compiler"
	"reconc.dev/reconc/internal/completiongate"
	"reconc.dev/reconc/internal/policyproof"
	"reconc.dev/reconc/internal/retention"
	"reconc.dev/reconc/internal/runtime"
	"reconc.dev/reconc/internal/runtime/agentsession"
)

func TestEvaluateMinimalReportAndDigest(t *testing.T) {
	repo := completionRepo(t, "rules: []\n", nil)
	report, err := completiongate.Evaluate(repo, completiongate.Options{})
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if !report.OK || report.Decision != "pass" || report.NextAction != "" {
		t.Fatalf("unexpected report: %#v", report)
	}
	if err := completiongate.VerifyReport(report); err != nil {
		t.Fatalf("VerifyReport: %v", err)
	}
	body, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), `"next_action"`) {
		t.Fatalf("passing report or individual checks exposed a next action: %s", body)
	}
	report.Checks[0].Detail = "tampered"
	if err := completiongate.VerifyReport(report); err == nil {
		t.Fatal("tampered report digest was accepted")
	}
}

func TestEvaluateRefusesPersistedEvidenceTaint(t *testing.T) {
	repo := completionRepo(t, "rules: []\n", nil)
	if result := agentsession.RunSessionStart(repo, []byte(`{"session_id":"tainted"}`)); result.ExitCode != 0 {
		t.Fatalf("session start: %+v", result)
	}
	command := strings.Repeat("x", 33*1024)
	payload, err := json.Marshal(map[string]interface{}{
		"session_id": "tainted",
		"tool_name":  "Bash",
		"tool_input": map[string]interface{}{"command": command},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result := agentsession.RunPostToolUse(repo, payload); result.ExitCode != 0 ||
		!strings.Contains(result.Stderr, "item_bytes") {
		t.Fatalf("overflow fixture was not tainted: %+v", result)
	}
	report := evaluateCompletion(t, repo, completiongate.Options{})
	assertFailedCheck(t, report, "session/evidence-complete")
}

func TestEvaluateBlocksStaleLockfile(t *testing.T) {
	repo := completionRepo(t, "rules: []\n", nil)
	writeCompletionFile(t, repo, "policies/rules.yml", "rules:\n  - id: changed\n    kind: deny_write\n    paths: ['x']\n    mode: block\n    message: changed\n")
	report := evaluateCompletion(t, repo, completiongate.Options{})
	assertFailedCheck(t, report, "policy/lockfile")
}

func TestCurrentBlockRequiresExplicitCorrectedPass(t *testing.T) {
	repo := completionRepo(t, denyGeneratedPolicy, nil)
	state, err := agentsession.CaptureCompletionState(repo)
	if err != nil {
		t.Fatal(err)
	}
	inputs := runtime.Empty()
	inputs.WritePaths = []string{"gen/output.go"}
	blocked, err := runtime.CheckRepoPolicy(state.RepoRoot, inputs)
	if err != nil {
		t.Fatal(err)
	}
	if err := policyproof.Store(state.RepoRoot, "check", state.Fingerprint, blocked); err != nil {
		t.Fatal(err)
	}
	report := evaluateCompletion(t, repo, completiongate.Options{WindowMinutes: 1_000_000})
	assertFailedCheck(t, report, "policy/unresolved/deny-generated")

	corrected, err := runtime.CheckRepoPolicy(state.RepoRoot, runtime.Empty())
	if err != nil {
		t.Fatal(err)
	}
	if err := policyproof.Store(state.RepoRoot, "check", state.Fingerprint, corrected); err != nil {
		t.Fatal(err)
	}
	report = evaluateCompletion(t, repo, completiongate.Options{})
	if !report.OK {
		t.Fatalf("corrected explicit pass did not supersede the block: %#v", report.Checks)
	}
}

func TestEvaluateFailsClosedOnTamperedDecisionProof(t *testing.T) {
	repo := completionRepo(t, denyGeneratedPolicy, nil)
	state, err := agentsession.CaptureCompletionState(repo)
	if err != nil {
		t.Fatal(err)
	}
	inputs := runtime.Empty()
	inputs.WritePaths = []string{"gen/output.go"}
	blocked, err := runtime.CheckRepoPolicy(state.RepoRoot, inputs)
	if err != nil {
		t.Fatal(err)
	}
	if err := policyproof.Store(state.RepoRoot, "check", state.Fingerprint, blocked); err != nil {
		t.Fatal(err)
	}
	path := policyproof.Path(state.RepoRoot)
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	body = []byte(strings.Replace(string(body), `"event": "check"`, `"event": "ci"`, 1))
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}
	report := evaluateCompletion(t, repo, completiongate.Options{})
	assertFailedCheck(t, report, "policy/latest-decision-integrity")
}

func TestEvaluateRejectsForgedUppercaseCurrentDecisionFingerprint(t *testing.T) {
	repo := completionRepo(t, denyGeneratedPolicy, nil)
	state, err := agentsession.CaptureCompletionState(repo)
	if err != nil {
		t.Fatal(err)
	}
	inputs := runtime.Empty()
	inputs.WritePaths = []string{"gen/output.go"}
	blocked, err := runtime.CheckRepoPolicy(state.RepoRoot, inputs)
	if err != nil {
		t.Fatal(err)
	}
	if err := policyproof.Store(state.RepoRoot, "check", state.Fingerprint, blocked); err != nil {
		t.Fatal(err)
	}
	path := policyproof.Path(state.RepoRoot)
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var record policyproof.Record
	if err := json.Unmarshal(body, &record); err != nil {
		t.Fatal(err)
	}
	record.CandidateFingerprint = strings.ToUpper(record.CandidateFingerprint)
	reportBody, err := json.Marshal(record.Report)
	if err != nil {
		t.Fatal(err)
	}
	payload := struct {
		Schema               string          `json:"schema"`
		FormatVersion        string          `json:"format_version"`
		Event                string          `json:"event"`
		RepoRoot             string          `json:"repo_root"`
		CandidateFingerprint string          `json:"candidate_fingerprint"`
		PolicyReportHash     string          `json:"policy_report_hash"`
		Report               json.RawMessage `json:"report"`
	}{
		Schema: record.Schema, FormatVersion: record.FormatVersion, Event: record.Event,
		RepoRoot: record.RepoRoot, CandidateFingerprint: record.CandidateFingerprint,
		PolicyReportHash: record.PolicyReportHash, Report: reportBody,
	}
	payloadBody, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(payloadBody)
	record.Digest = hex.EncodeToString(digest[:])
	body, err = json.MarshalIndent(record, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(body, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}

	report := evaluateCompletion(t, repo, completiongate.Options{})
	assertFailedCheck(t, report, "policy/latest-decision-integrity")
}

func TestCommandProofMustMatchCurrentStagedCandidate(t *testing.T) {
	repo := completionRepo(t, requireGoTestPolicy, map[string]string{"src/main.go": "package main\n"})
	initCompletionGit(t, repo)
	writeCompletionFile(t, repo, "src/main.go", "package main\n\nconst version = 1\n")
	gitCompletion(t, repo, "add", "src/main.go")

	report := evaluateCompletion(t, repo, completiongate.Options{})
	assertFailedCheck(t, report, "policy/current/tests-must-pass")

	snapshot, err := commandproof.CaptureStagedClean(repo)
	if err != nil {
		t.Fatal(err)
	}
	completedAt := time.Now().UTC()
	if _, err := commandproof.StoreSuccess(snapshot, "go test", "shell", completedAt.Add(-time.Second), completedAt); err != nil {
		t.Fatal(err)
	}
	report = evaluateCompletion(t, repo, completiongate.Options{})
	if !report.OK {
		t.Fatalf("current staged command proof was rejected: %#v", report.Checks)
	}

	writeCompletionFile(t, repo, "src/main.go", "package main\n\nconst version = 2\n")
	gitCompletion(t, repo, "add", "src/main.go")
	report = evaluateCompletion(t, repo, completiongate.Options{})
	assertFailedCheck(t, report, "policy/current/tests-must-pass")
}

func TestTamperedCommandProofCannotSatisfyCompletion(t *testing.T) {
	repo := completionRepo(t, requireGoTestPolicy, map[string]string{"src/main.go": "package main\n"})
	initCompletionGit(t, repo)
	writeCompletionFile(t, repo, "src/main.go", "package main\n\nconst version = 1\n")
	gitCompletion(t, repo, "add", "src/main.go")
	snapshot, err := commandproof.CaptureStagedClean(repo)
	if err != nil {
		t.Fatal(err)
	}
	completedAt := time.Now().UTC()
	if _, err := commandproof.StoreSuccess(snapshot, "go test", "shell", completedAt.Add(-time.Second), completedAt); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(retention.ProjectDir(retention.ResolveStateRoot(), snapshot.RepoRoot), "command-proofs")
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 1 {
		t.Fatalf("command proof fixture: entries=%d err=%v", len(entries), err)
	}
	path := filepath.Join(dir, entries[0].Name())
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	body = []byte(strings.Replace(string(body), `"command": "go test"`, `"command": "go test ./..."`, 1))
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}

	report := evaluateCompletion(t, repo, completiongate.Options{})
	assertFailedCheck(t, report, "policy/current/tests-must-pass")
}

func TestTypedTaskCompletionAndRequiredEvidence(t *testing.T) {
	config := "task_lifecycle:\n  profile: sections-v1\n  completion:\n    required_evidence_fields: [Tests]\n"
	files := map[string]string{
		".reconc.yml":                  config,
		"docs/tasks.md":                "# TASK Control Plane\n\n## Active\n\n- [~] 001 Ship proof -> tasks/001-ship-proof.md\n\n## Queue\n\n## Blocked\n\n## Done\n",
		"docs/tasks/001-ship-proof.md": taskDetail("- [~] Prove it", ""),
	}
	repo := completionRepo(t, "rules: []\n", files)
	report := evaluateCompletion(t, repo, completiongate.Options{})
	assertFailedCheck(t, report, "task/completion/open-subtask")

	writeCompletionFile(t, repo, "docs/tasks/001-ship-proof.md", taskDetail("- [~] Prove it", ""))
	report = evaluateCompletion(t, repo, completiongate.Options{})
	assertFailedCheck(t, report, "task/completion/missing-evidence")

	writeCompletionFile(t, repo, "docs/tasks/001-ship-proof.md", taskDetail("- [x] Prove it", "- Tests: go test ./..."))
	report = evaluateCompletion(t, repo, completiongate.Options{})
	if !report.OK || report.TaskID != "001" {
		t.Fatalf("complete typed TASK was rejected: %#v", report)
	}
}

func TestTypedTaskTerminalAndCommittedStateContracts(t *testing.T) {
	t.Run("queued work remains", func(t *testing.T) {
		files := map[string]string{
			".reconc.yml":              "task_lifecycle:\n  profile: sections-v1\n",
			"docs/tasks.md":            "# TASK Control Plane\n\n## Active\n\n## Queue\n\n- [ ] 001 Queued -> tasks/001-queued.md\n\n## Blocked\n\n## Done\n",
			"docs/tasks/001-queued.md": "# TASK 001: Queued\n\n## Why\n\nWait.\n\n## Acceptance\n\n- Done.\n\n## Sub-Tasks\n\n- [ ] Work\n\n## Notes\n\nNone.\n\n## Deviations\n\nNone.\n",
		}
		report := evaluateCompletion(t, completionRepo(t, "rules: []\n", files), completiongate.Options{})
		assertFailedCheck(t, report, "task/terminal")
	})

	t.Run("blocked work remains", func(t *testing.T) {
		files := map[string]string{
			".reconc.yml":               "task_lifecycle:\n  profile: sections-v1\n",
			"docs/tasks.md":             "# TASK Control Plane\n\n## Active\n\n## Queue\n\n## Blocked\n\n- [!] 001 Blocked -> tasks/001-blocked.md\n\n## Done\n",
			"docs/tasks/001-blocked.md": "# TASK 001: Blocked\n\n## Why\n\nWait.\n\n## Acceptance\n\n- Done.\n\n## Sub-Tasks\n\n- [ ] Work\n\n## Blocker\n\nExternal dependency.\n\n## Notes\n\nNone.\n\n## Deviations\n\nNone.\n",
		}
		report := evaluateCompletion(t, completionRepo(t, "rules: []\n", files), completiongate.Options{})
		assertFailedCheck(t, report, "task/terminal")
	})

	t.Run("terminal board passes", func(t *testing.T) {
		files := map[string]string{
			".reconc.yml":                 "task_lifecycle:\n  profile: sections-v1\n",
			"docs/tasks.md":               "# TASK Control Plane\n\n## Active\n\n## Queue\n\n## Blocked\n\n## Done\n\n- [x] 001 Done -> tasks/done/001-done.md\n",
			"docs/tasks/done/001-done.md": "# TASK 001: Done\n\n## Why\n\nComplete.\n\n## Acceptance\n\n- Done.\n\n## Sub-Tasks\n\n- [x] Work\n\n## Notes\n\nNone.\n\n## Deviations\n\nNone.\n",
		}
		report := evaluateCompletion(t, completionRepo(t, "rules: []\n", files), completiongate.Options{})
		if !report.OK {
			t.Fatalf("terminal TASK board was rejected: %+v", report.Checks)
		}
	})

	t.Run("committed task plane is mandatory", func(t *testing.T) {
		config := "task_lifecycle:\n  profile: sections-v1\n  completion:\n    require_committed: true\n"
		files := map[string]string{
			".reconc.yml":                  config,
			"docs/tasks.md":                "# TASK Control Plane\n\n## Active\n\n- [~] 001 Ship proof -> tasks/001-ship-proof.md\n\n## Queue\n\n## Blocked\n\n## Done\n",
			"docs/tasks/001-ship-proof.md": taskDetail("- [x] Prove it", ""),
		}
		repo := completionRepo(t, "rules: []\n", files)
		report := evaluateCompletion(t, repo, completiongate.Options{})
		assertFailedCheck(t, report, "task/committed")

		initCompletionGit(t, repo)
		report = evaluateCompletion(t, repo, completiongate.Options{})
		if !report.OK {
			t.Fatalf("committed TASK plane was rejected: %+v", report.Checks)
		}
		writeCompletionFile(t, repo, "docs/tasks/001-ship-proof.md", taskDetail("- [x] Prove it", "")+"\n")
		report = evaluateCompletion(t, repo, completiongate.Options{})
		assertFailedCheck(t, report, "task/committed")
	})
}

func TestCleanGitIsExplicitOptIn(t *testing.T) {
	repo := completionRepo(t, "rules: []\n", nil)
	initCompletionGit(t, repo)
	writeCompletionFile(t, repo, "scratch.txt", "dirty\n")
	if report := evaluateCompletion(t, repo, completiongate.Options{}); !report.OK {
		t.Fatalf("dirty Git unexpectedly replaced completion evidence: %#v", report.Checks)
	}
	report := evaluateCompletion(t, repo, completiongate.Options{RequireCleanGit: true})
	assertFailedCheck(t, report, "git/clean")
}

func TestEvaluateFailsClosedWhenGitStatusIsUnreadable(t *testing.T) {
	if goruntime.GOOS == "windows" {
		t.Skip("fixture uses a POSIX shell wrapper")
	}
	repo := completionRepo(t, "rules: []\n", nil)
	initCompletionGit(t, repo)
	realGit, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	wrapper := filepath.Join(bin, "git")
	quotedGit := strings.ReplaceAll(realGit, "'", "'\\''")
	body := "#!/bin/sh\nfor arg in \"$@\"; do\n  if [ \"$arg\" = status ]; then\n    printf 'forced status failure\\n' >&2\n    exit 73\n  fi\ndone\nexec '" + quotedGit + "' \"$@\"\n"
	if err := os.WriteFile(wrapper, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	report := evaluateCompletion(t, repo, completiongate.Options{RequireCleanGit: true})
	assertFailedCheck(t, report, "git/status")
	assertFailedCheck(t, report, "git/clean")
	if len(report.Candidate.DirtyPaths) != 0 {
		t.Fatalf("unreadable Git status leaked error text as dirty paths: %#v", report.Candidate.DirtyPaths)
	}
}

func TestEvaluateFailsClosedWhenGitHeadIsUnreadable(t *testing.T) {
	if goruntime.GOOS == "windows" {
		t.Skip("fixture uses a POSIX shell wrapper")
	}
	repo := completionRepo(t, "rules: []\n", nil)
	realGit, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	wrapper := filepath.Join(bin, "git")
	quotedGit := strings.ReplaceAll(realGit, "'", "'\\''")
	body := "#!/bin/sh\nfor arg in \"$@\"; do\n  case \"$arg\" in\n    status|ls-files) exit 0 ;;\n  esac\ndone\nexec '" + quotedGit + "' \"$@\"\n"
	if err := os.WriteFile(wrapper, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	report := evaluateCompletion(t, repo, completiongate.Options{})
	assertFailedCheck(t, report, "git/status")
	if !report.Candidate.GitAvailable || !strings.HasPrefix(report.Candidate.GitHead, "error:") {
		t.Fatalf("unreadable Git HEAD was misclassified as a non-Git repository: %#v", report.Candidate)
	}
}

func completionRepo(t *testing.T, policyText string, files map[string]string) string {
	t.Helper()
	t.Setenv("RECONC_HOME", t.TempDir())
	repo := t.TempDir()
	writeCompletionFile(t, repo, "AGENTS.md", "# fixture\n")
	writeCompletionFile(t, repo, "policies/rules.yml", policyText)
	for path, body := range files {
		writeCompletionFile(t, repo, path, body)
	}
	if _, err := compiler.CompileRepoPolicy(repo, "completion-test"); err != nil {
		t.Fatalf("compile fixture: %v", err)
	}
	return repo
}

func evaluateCompletion(t *testing.T, repo string, options completiongate.Options) *completiongate.Report {
	t.Helper()
	report, err := completiongate.Evaluate(repo, options)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if err := completiongate.VerifyReport(report); err != nil {
		t.Fatalf("VerifyReport: %v", err)
	}
	return report
}

func assertFailedCheck(t *testing.T, report *completiongate.Report, id string) {
	t.Helper()
	for _, check := range report.Checks {
		if check.ID == id && check.Status == completiongate.StatusFail {
			if report.OK || strings.TrimSpace(report.NextAction) == "" {
				t.Fatalf("failed report lacks block decision or next action: %#v", report)
			}
			return
		}
	}
	t.Fatalf("missing failed check %q: %#v", id, report.Checks)
}

func writeCompletionFile(t *testing.T, repo, relative, body string) {
	t.Helper()
	path := filepath.Join(repo, filepath.FromSlash(relative))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func initCompletionGit(t *testing.T, repo string) {
	t.Helper()
	gitCompletion(t, repo, "init")
	gitCompletion(t, repo, "config", "user.name", "reconc-test")
	gitCompletion(t, repo, "config", "user.email", "reconc-test@example.com")
	gitCompletion(t, repo, "add", "-A")
	gitCompletion(t, repo, "commit", "-m", "fixture")
}

func gitCompletion(t *testing.T, repo string, args ...string) {
	t.Helper()
	command := exec.Command("git", args...)
	command.Dir = repo
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, output)
	}
}

func taskDetail(subTask, evidence string) string {
	return "# TASK 001: Ship proof\n\n## Why\n\nProve completion.\n\n## Acceptance\n\n- Evidence is current.\n\n## Sub-Tasks\n\n" + subTask + "\n\n## Evidence\n\n" + evidence + "\n\n## Notes\n\nNone.\n\n## Deviations\n\nNone.\n"
}

const denyGeneratedPolicy = "rules:\n  - id: deny-generated\n    kind: deny_write\n    paths: ['gen/**']\n    mode: block\n    message: generated output is blocked\n"

const requireGoTestPolicy = "rules:\n  - id: tests-must-pass\n    kind: require_command_success\n    when_paths: ['src/**']\n    commands: ['go test']\n    mode: block\n    message: tests must pass\n"
