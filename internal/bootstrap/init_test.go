package bootstrap

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInitializeFreshRepositoryIsRecordedAndIdempotent(t *testing.T) {
	bootstrapTestHome(t)
	repo := t.TempDir()
	first, err := Initialize(InitRequest{RepoRoot: repo, NoHooks: true}, "test-version")
	if err != nil {
		t.Fatalf("first init: %v", err)
	}
	if first.Status != InitComplete || !first.Changed || first.Profile != ProfileMinimal ||
		first.PlanDigest == nil || first.ReceiptPath == nil {
		t.Fatalf("first init result = %+v", first)
	}
	recorded := filepath.Join(repo, ".reconc", "bootstrap-plan-"+*first.PlanDigest+".json")
	if _, err := os.Stat(recorded); err != nil {
		t.Fatalf("durable plan missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(repo, filepath.FromSlash(*first.ReceiptPath))); err != nil {
		t.Fatalf("transaction receipt missing: %v", err)
	}

	second, err := Initialize(InitRequest{RepoRoot: repo}, "test-version")
	if err != nil {
		t.Fatalf("idempotent init: %v", err)
	}
	if second.Status != InitComplete || second.Changed || second.Profile != ProfileMinimal ||
		second.ReceiptPath == nil || *second.ReceiptPath != *first.ReceiptPath {
		t.Fatalf("idempotent init result = %+v", second)
	}
}

func TestInitializeAdvancedMaterializesAndReceiptsEmbeddedHarness(t *testing.T) {
	bootstrapTestHome(t)
	repo := t.TempDir()
	report, err := Initialize(InitRequest{
		RepoRoot: repo, Profile: ProfileAdvanced, NoHooks: true,
	}, "0.9.0")
	if err != nil {
		t.Fatal(err)
	}
	if report.Status != InitComplete || len(report.HarnessPacks) != 1 ||
		report.HarnessPacks[0].Name != "advanced" ||
		report.HarnessPacks[0].Version != "1.0.0" {
		t.Fatalf("advanced init result = %+v", report)
	}
	for _, relative := range []string{
		"tools/reconc/harness/template/BOOTSTRAP.md",
		"tools/reconc/harness/template/audits/run-workflow-audit",
		"tools/reconc/harness/template/utils/task-claim/main.go",
	} {
		if _, err := os.Stat(filepath.Join(repo, filepath.FromSlash(relative))); err != nil {
			t.Fatalf("advanced harness file missing %s: %v", relative, err)
		}
	}
	if _, err := os.Stat(filepath.Join(repo, "tools", "reconc", "harness", "template", "coverage.out")); !os.IsNotExist(err) {
		t.Fatalf("advanced init materialized generated coverage output: %v", err)
	}
	plan, err := LoadPlan(filepath.Join(repo, ".reconc", "bootstrap-plan-"+*report.PlanDigest+".json"))
	if err != nil {
		t.Fatal(err)
	}
	receipt, _, err := loadInstallReceipt(plan)
	if err != nil {
		t.Fatal(err)
	}
	if len(receipt.HarnessPacks) != 1 || receipt.HarnessPacks[0] != report.HarnessPacks[0] {
		t.Fatalf("advanced receipt lost pack identity: %+v", receipt)
	}
}

func TestInitializeMatureRepositoryRequiresExplicitProfileWithoutWriting(t *testing.T) {
	bootstrapTestHome(t)
	repo := t.TempDir()
	writeBootstrapTestFile(t, repo, "AGENTS.md", "# User contract\n", 0o644)
	before := bootstrapTreeSnapshot(t, repo)

	report, err := Initialize(InitRequest{RepoRoot: repo}, "test-version")
	if err == nil || report.Status != InitRefused ||
		!strings.Contains(report.NextAction, "--profile minimal") {
		t.Fatalf("ambiguous init = %+v err=%v", report, err)
	}
	after := bootstrapTreeSnapshot(t, repo)
	if strings.Join(before, "\n") != strings.Join(after, "\n") {
		t.Fatalf("refused init mutated repository: before=%v after=%v", before, after)
	}
}

func TestInitializeManagedBlockAcceptancePreservesUserBytes(t *testing.T) {
	bootstrapTestHome(t)
	repo := t.TempDir()
	original := "# User contract\n\nKeep this exact.\n"
	writeBootstrapTestFile(t, repo, "AGENTS.md", original, 0o644)

	drift, err := Initialize(InitRequest{
		RepoRoot: repo, Profile: ProfileMinimal, NoHooks: true,
	}, "test-version")
	if err != nil || drift.Status != InitDrift || len(drift.Candidates) != 1 ||
		!strings.Contains(drift.NextAction, "--accept-managed-blocks") {
		t.Fatalf("drift init = %+v err=%v", drift, err)
	}
	body, err := os.ReadFile(filepath.Join(repo, "AGENTS.md"))
	if err != nil || string(body) != original {
		t.Fatalf("drift changed user document: body=%q err=%v", body, err)
	}

	accepted, err := Initialize(InitRequest{
		RepoRoot: repo, Profile: ProfileMinimal, NoHooks: true, AcceptManagedBlocks: true,
	}, "test-version")
	if err != nil || accepted.Status != InitComplete {
		t.Fatalf("accepted init = %+v err=%v", accepted, err)
	}
	body, err = os.ReadFile(filepath.Join(repo, "AGENTS.md"))
	if err != nil || !strings.HasPrefix(string(body), original) ||
		!strings.Contains(string(body), agentBlockStart) {
		t.Fatalf("accepted init lost user bytes: body=%q err=%v", body, err)
	}
}

func TestInitializeRejectsConcurrentRepositoryTransaction(t *testing.T) {
	bootstrapTestHome(t)
	repo := t.TempDir()
	root, err := canonicalRepoRoot(repo)
	if err != nil {
		t.Fatal(err)
	}
	locked := make(chan struct{})
	release := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- withRepositoryTransactionLock(root, func() error {
			close(locked)
			<-release
			return nil
		})
	}()
	<-locked

	report, err := Initialize(InitRequest{RepoRoot: repo, NoHooks: true}, "test-version")
	if err == nil || !strings.Contains(err.Error(), "already active") || report.Changed {
		t.Fatalf("concurrent init = %+v err=%v", report, err)
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatalf("lock holder failed: %v", err)
	}
}

func TestDirectBootstrapApplyRejectsConcurrentRepositoryTransaction(t *testing.T) {
	bootstrapTestHome(t)
	repo := t.TempDir()
	plan, err := BuildPlan(Request{
		RepoRoot: repo, Profile: ProfileMinimal,
	}, "test-version")
	if err != nil {
		t.Fatal(err)
	}
	locked := make(chan struct{})
	release := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- withRepositoryTransactionLock(plan.RepoRoot, func() error {
			close(locked)
			<-release
			return nil
		})
	}()
	<-locked

	report, err := Apply(plan, "test-version")
	if err == nil || !strings.Contains(err.Error(), "already active") ||
		report.Status != ApplyRolledBack || len(report.Created) != 0 {
		t.Fatalf("concurrent direct apply = %+v err=%v", report, err)
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatalf("lock holder failed: %v", err)
	}
}

func TestInitializeWholeFileDriftNamesTheCandidateReviewAction(t *testing.T) {
	bootstrapTestHome(t)
	repo := t.TempDir()
	writeBootstrapTestFile(t, repo, ".reconc.yml", "rules: []\n", 0o644)

	report, err := Initialize(InitRequest{
		RepoRoot: repo, Profile: ProfileMinimal, NoHooks: true,
	}, "test-version")
	if err != nil || report.Status != InitDrift || len(report.Candidates) == 0 {
		t.Fatalf("whole-file drift = %+v err=%v", report, err)
	}
	if !strings.Contains(report.NextAction, "review "+report.Candidates[0]) ||
		!strings.Contains(report.NextAction, "--profile minimal") {
		t.Fatalf("whole-file drift next action = %q", report.NextAction)
	}
	body, readErr := os.ReadFile(filepath.Join(repo, ".reconc.yml"))
	if readErr != nil || string(body) != "rules: []\n" {
		t.Fatalf("whole-file drift changed policy: body=%q err=%v", body, readErr)
	}
}

func TestInitializeRejectsSymlinkedRepositoryLockDirectory(t *testing.T) {
	home := t.TempDir()
	target := t.TempDir()
	lockDirectory := filepath.Join(home, "locks", "repositories")
	if err := os.MkdirAll(filepath.Dir(lockDirectory), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, lockDirectory); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	t.Setenv("RECONC_HOME", home)
	repo := t.TempDir()

	report, err := Initialize(InitRequest{RepoRoot: repo, NoHooks: true}, "test-version")
	if err == nil || !strings.Contains(err.Error(), "not a real directory") || report.Changed {
		t.Fatalf("symlinked lock directory = %+v err=%v", report, err)
	}
	if entries, readErr := os.ReadDir(repo); readErr != nil || len(entries) != 0 {
		t.Fatalf("rejected lock mutated repository: entries=%v err=%v", entries, readErr)
	}
}

func TestInitializeRejectsContradictoryHookSelection(t *testing.T) {
	bootstrapTestHome(t)
	repo := t.TempDir()
	report, err := Initialize(InitRequest{
		RepoRoot: repo, Profile: ProfileMinimal, Hooks: []string{"codex"},
		HooksExplicit: true, NoHooks: true,
	}, "test-version")
	if err == nil || !strings.Contains(err.Error(), "mutually exclusive") ||
		report.Status != InitRefused || report.Changed {
		t.Fatalf("contradictory hook selection = %+v err=%v", report, err)
	}
	if entries, readErr := os.ReadDir(repo); readErr != nil || len(entries) != 0 {
		t.Fatalf("contradictory hook selection mutated repository: entries=%v err=%v", entries, readErr)
	}
}

func TestInitRemediationSelectionIsDeterministic(t *testing.T) {
	if got := suggestedExplicitProfile(nil); got != ProfileMinimal {
		t.Fatalf("nil inspection profile = %s", got)
	}
	governed := &Inspection{ExistingPaths: []string{
		".reconc.yml", "docs/tasks.md", "docs/documentation.md",
	}}
	if got := suggestedExplicitProfile(governed); got != ProfileGoverned {
		t.Fatalf("governed inspection profile = %s", got)
	}
	command := renderInitCommand("/tmp/repo with spaces", ProfileAdvanced,
		[]string{"default", "strict"}, nil, true)
	for _, expected := range []string{
		"reconc init", "\"/tmp/repo with spaces\"", "--profile advanced",
		"--pack \"strict\"", "--no-hooks",
	} {
		if !strings.Contains(command, expected) {
			t.Fatalf("init remediation %q omits %q", command, expected)
		}
	}
	if strings.Contains(command, "--pack default") {
		t.Fatalf("init remediation repeated implicit default pack: %q", command)
	}
	hookCommand := renderInitCommand("/tmp/repo", ProfileGoverned,
		[]string{"agent", "strict"}, []string{"codex"}, false)
	if !strings.Contains(hookCommand, "--pack \"strict\"") ||
		!strings.Contains(hookCommand, "--hook \"codex\"") ||
		strings.Contains(hookCommand, "--pack agent") ||
		strings.Contains(hookCommand, "--no-hooks") {
		t.Fatalf("hook remediation = %q", hookCommand)
	}
	fallback := initDriftNext(&Plan{}, &Report{NextAction: "review manually"})
	if fallback != "review manually" {
		t.Fatalf("drift fallback = %q", fallback)
	}
}
