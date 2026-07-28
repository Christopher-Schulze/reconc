package bootstrap

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"

	"reconc.dev/reconc/internal/commandproof"
	"reconc.dev/reconc/internal/compiler"
)

const syncTestVersion = "0.9.0"

func TestRepositoryReceiptAndSyncAreIdempotent(t *testing.T) {
	repo, receipt := initializeSyncFixture(t, ProfileAdvanced)
	if receipt.Generation != 1 || receipt.ProductVersion != syncTestVersion {
		t.Fatalf("initial receipt = %+v", receipt)
	}
	verification, err := VerifyRepository(repo, syncTestVersion)
	if err != nil || !verification.Valid {
		t.Fatalf("verify repository: %+v err=%v", verification, err)
	}
	plan, err := BuildSyncPlan(repo, syncTestVersion)
	if err != nil {
		t.Fatal(err)
	}
	for _, action := range plan.Actions {
		if action.State != SyncUnchanged {
			t.Fatalf("fresh repository action = %+v", action)
		}
	}
	first, err := encodeSyncPlan(plan)
	if err != nil {
		t.Fatal(err)
	}
	second, err := BuildSyncPlan(repo, syncTestVersion)
	if err != nil {
		t.Fatal(err)
	}
	secondBody, err := encodeSyncPlan(second)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, secondBody) {
		t.Fatal("read-only sync planning is not deterministic")
	}
	report, err := ApplySyncPlan(plan, plan.PlanDigest, syncTestVersion)
	if err != nil || report.Status != SyncComplete || len(report.Changed) != 0 {
		t.Fatalf("idempotent sync apply = %+v err=%v", report, err)
	}
	after, err := LoadRepositoryReceipt(repo)
	if err != nil {
		t.Fatal(err)
	}
	if after.Generation != receipt.Generation || after.ReceiptDigest != receipt.ReceiptDigest {
		t.Fatalf("no-op sync advanced receipt: before=%+v after=%+v", receipt, after)
	}
}

func TestRepositoryVerificationDetectsOwnedDrift(t *testing.T) {
	t.Run("missing receipt", func(t *testing.T) {
		repo, _ := initializeSyncFixture(t, ProfileGoverned)
		if err := os.Remove(filepath.Join(repo, filepath.FromSlash(RepositoryReceiptRelativePath))); err != nil {
			t.Fatal(err)
		}
		verification, err := VerifyRepository(repo, syncTestVersion)
		if err != nil || verification.Valid || len(verification.Checks) != 1 {
			t.Fatalf("missing receipt verification = %+v err=%v", verification, err)
		}
	})
	t.Run("product version", func(t *testing.T) {
		repo, _ := initializeSyncFixture(t, ProfileGoverned)
		verification, err := VerifyRepository(repo, "9.9.9")
		if err != nil || verification.Valid {
			t.Fatalf("version verification = %+v err=%v", verification, err)
		}
	})
	t.Run("managed file", func(t *testing.T) {
		repo, receipt := initializeSyncFixture(t, ProfileAdvanced)
		managed := receipt.ManagedFiles[firstHarnessManagedFile(t, receipt)]
		if err := os.WriteFile(
			filepath.Join(repo, filepath.FromSlash(managed.Path)),
			[]byte("managed drift\n"),
			os.FileMode(managed.Mode),
		); err != nil {
			t.Fatal(err)
		}
		verification, err := VerifyRepository(repo, syncTestVersion)
		if err != nil || verification.Valid {
			t.Fatalf("managed file verification = %+v err=%v", verification, err)
		}
	})
	t.Run("managed file mode", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("Windows does not expose portable POSIX permission drift")
		}
		repo, receipt := initializeSyncFixture(t, ProfileAdvanced)
		managed := receipt.ManagedFiles[firstHarnessManagedFile(t, receipt)]
		if err := os.Chmod(filepath.Join(repo, filepath.FromSlash(managed.Path)), 0o600); err != nil {
			t.Fatal(err)
		}
		verification, err := VerifyRepository(repo, syncTestVersion)
		if err != nil || verification.Valid {
			t.Fatalf("managed file mode verification = %+v err=%v", verification, err)
		}
	})
	t.Run("managed block", func(t *testing.T) {
		repo, _ := initializeSyncFixture(t, ProfileGoverned)
		path := filepath.Join(repo, "AGENTS.md")
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		body = bytes.Replace(body, []byte("reconc refresh ."), []byte("reconc compile ."), 1)
		if err := os.WriteFile(path, body, 0o644); err != nil {
			t.Fatal(err)
		}
		verification, err := VerifyRepository(repo, syncTestVersion)
		if err != nil || verification.Valid {
			t.Fatalf("managed block verification = %+v err=%v", verification, err)
		}
	})
	t.Run("generated artifact", func(t *testing.T) {
		repo, _ := initializeSyncFixture(t, ProfileGoverned)
		if err := os.WriteFile(
			filepath.Join(repo, ".reconc", "policy.lock.json"),
			[]byte("{}\n"),
			0o644,
		); err != nil {
			t.Fatal(err)
		}
		verification, err := VerifyRepository(repo, syncTestVersion)
		if err != nil || verification.Valid {
			t.Fatalf("generated artifact verification = %+v err=%v", verification, err)
		}
	})
}

func TestRepositorySyncCapturesGitAndVerifiesConfiguredHook(t *testing.T) {
	repo := t.TempDir()
	t.Setenv("RECONC_HOME", t.TempDir())
	command := exec.Command("git", "init", repo)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, output)
	}
	report, err := Initialize(InitRequest{
		RepoRoot: repo, Profile: ProfileGoverned, SkipAgentHooks: true,
	}, syncTestVersion)
	if err != nil || report.Status != InitComplete {
		t.Fatalf("initialize Git repository: %+v err=%v", report, err)
	}
	plan, err := BuildSyncPlan(repo, syncTestVersion)
	if err != nil {
		t.Fatal(err)
	}
	if plan.GitSnapshot == nil || plan.GitSnapshot.RepoRoot != report.RepoRoot {
		t.Fatalf("Git-bound sync plan = %+v", plan.GitSnapshot)
	}
	verification, err := VerifyRepository(repo, syncTestVersion)
	if err != nil || !verification.Valid {
		t.Fatalf("hook verification = %+v err=%v", verification, err)
	}
	foundHook := false
	for _, check := range verification.Checks {
		if strings.HasPrefix(check.Name, "hook:") && check.Status == "PASS" {
			foundHook = true
		}
	}
	if !foundHook {
		t.Fatalf("configured hook was not verified: %+v", verification.Checks)
	}
	if err := os.Remove(filepath.Join(repo, ".git", "hooks", "pre-commit")); err != nil {
		t.Fatal(err)
	}
	verification, err = VerifyRepository(repo, syncTestVersion)
	if err != nil || verification.Valid {
		t.Fatalf("missing hook verification = %+v err=%v", verification, err)
	}
}

func TestRepositorySyncReplacesOnlyExactOwnedBytes(t *testing.T) {
	repo, receipt := initializeSyncFixture(t, ProfileAdvanced)
	index := firstHarnessManagedFile(t, receipt)
	managed := receipt.ManagedFiles[index]
	target := filepath.Join(repo, filepath.FromSlash(managed.Path))
	desired, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	old := []byte("legacy harness bytes\n")
	if err := os.WriteFile(target, old, os.FileMode(managed.Mode)); err != nil {
		t.Fatal(err)
	}
	receipt.ManagedFiles[index].SHA256 = bytesSHA256(old)
	rewriteTestReceipt(t, repo, receipt, "0.8.8")

	plan, err := BuildSyncPlan(repo, syncTestVersion)
	if err != nil {
		t.Fatal(err)
	}
	action := syncActionForPath(t, plan, managed.Path)
	if action.State != SyncReplaceOwned || action.CurrentSHA256 != bytesSHA256(old) {
		t.Fatalf("replacement action = %+v", action)
	}
	report, err := ApplySyncPlan(plan, plan.PlanDigest, syncTestVersion)
	if err != nil || report.Status != SyncComplete {
		t.Fatalf("apply sync: %+v err=%v", report, err)
	}
	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, desired) {
		t.Fatal("owned harness bytes were not restored from the embedded pack")
	}
	updated, err := LoadRepositoryReceipt(repo)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Generation != receipt.Generation+1 || updated.ProductVersion != syncTestVersion {
		t.Fatalf("advanced receipt = %+v", updated)
	}
}

func TestRepositorySyncPreservesManagedBlockOutsideBytes(t *testing.T) {
	repo, receipt := initializeSyncFixture(t, ProfileGoverned)
	index := managedBlockIndex(t, receipt, ".gitignore")
	block := receipt.ManagedBlocks[index]
	target := filepath.Join(repo, ".gitignore")
	current, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	legacy := bytes.Replace(current, []byte("!.reconc/install.lock.json\n"), nil, 1)
	legacy = append([]byte("user-owned-ignore\n\n"), legacy...)
	if err := os.WriteFile(target, legacy, 0o644); err != nil {
		t.Fatal(err)
	}
	legacyBlock, err := extractManagedBlock(legacy, block.BlockStart, block.BlockEnd)
	if err != nil {
		t.Fatal(err)
	}
	receipt.ManagedBlocks[index].ManagedSHA256 = bytesSHA256(legacyBlock)
	receipt.ManagedBlocks[index].WholeFileSHA256 = bytesSHA256(legacy)
	rewriteTestReceipt(t, repo, receipt, "0.8.8")

	plan, err := BuildSyncPlan(repo, syncTestVersion)
	if err != nil {
		t.Fatal(err)
	}
	action := syncActionForPath(t, plan, ".gitignore")
	if action.State != SyncUpdateManagedBlock {
		t.Fatalf("managed-block action = %+v", action)
	}
	if _, err := ApplySyncPlan(plan, plan.PlanDigest, syncTestVersion); err != nil {
		t.Fatal(err)
	}
	updated, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(updated, []byte("user-owned-ignore\n\n")) ||
		!bytes.Contains(updated, []byte("!.reconc/install.lock.json\n")) {
		t.Fatalf("managed block update lost outside bytes or target rule:\n%s", updated)
	}
}

func TestRepositorySyncRefusesDriftAndStalePlans(t *testing.T) {
	repo, receipt := initializeSyncFixture(t, ProfileAdvanced)
	index := firstHarnessManagedFile(t, receipt)
	managed := receipt.ManagedFiles[index]
	target := filepath.Join(repo, filepath.FromSlash(managed.Path))
	if err := os.WriteFile(target, []byte("unreceipted user drift\n"), os.FileMode(managed.Mode)); err != nil {
		t.Fatal(err)
	}
	plan, err := BuildSyncPlan(repo, syncTestVersion)
	if err != nil {
		t.Fatal(err)
	}
	action := syncActionForPath(t, plan, managed.Path)
	if action.State != SyncUserDrift || len(plan.BlockingIssues) == 0 {
		t.Fatalf("drift action = %+v issues=%v", action, plan.BlockingIssues)
	}
	if _, err := os.Stat(filepath.Join(repo, filepath.FromSlash(action.CandidatePath))); !os.IsNotExist(err) {
		t.Fatalf("read-only plan materialized a candidate: %v", err)
	}
	blocked, err := ApplySyncPlan(plan, plan.PlanDigest, syncTestVersion)
	if err == nil || blocked.Status != SyncRefused || blocked.NextAction == "" {
		t.Fatalf("blocking plan apply = %+v err=%v", blocked, err)
	}

	if err := os.WriteFile(target, []byte("legacy\n"), os.FileMode(managed.Mode)); err != nil {
		t.Fatal(err)
	}
	receipt.ManagedFiles[index].SHA256 = bytesSHA256([]byte("legacy\n"))
	rewriteTestReceipt(t, repo, receipt, "0.8.8")
	stale, err := BuildSyncPlan(repo, syncTestVersion)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte("changed after plan\n"), os.FileMode(managed.Mode)); err != nil {
		t.Fatal(err)
	}
	report, err := ApplySyncPlan(stale, stale.PlanDigest, syncTestVersion)
	if err == nil || report.Status != SyncRefused {
		t.Fatalf("stale plan result = %+v err=%v", report, err)
	}
}

func TestRepositorySyncRollsBackOwnedMutation(t *testing.T) {
	repo, receipt := initializeSyncFixture(t, ProfileAdvanced)
	index := firstHarnessManagedFile(t, receipt)
	managed := receipt.ManagedFiles[index]
	target := filepath.Join(repo, filepath.FromSlash(managed.Path))
	old := []byte("rollback sentinel\n")
	if err := os.WriteFile(target, old, os.FileMode(managed.Mode)); err != nil {
		t.Fatal(err)
	}
	receipt.ManagedFiles[index].SHA256 = bytesSHA256(old)
	rewriteTestReceipt(t, repo, receipt, "0.8.8")
	beforeReceipt, err := os.ReadFile(filepath.Join(repo, filepath.FromSlash(RepositoryReceiptRelativePath)))
	if err != nil {
		t.Fatal(err)
	}
	plan, err := BuildSyncPlan(repo, syncTestVersion)
	if err != nil {
		t.Fatal(err)
	}
	report, err := applySyncPlan(plan, plan.PlanDigest, syncTestVersion, syncApplyOptions{failAfter: 1})
	if err == nil || report.Status != SyncRolledBack {
		t.Fatalf("injected apply = %+v err=%v", report, err)
	}
	after, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(after, old) {
		t.Fatal("owned file was not rolled back")
	}
	afterReceipt, err := os.ReadFile(filepath.Join(repo, filepath.FromSlash(RepositoryReceiptRelativePath)))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(beforeReceipt, afterReceipt) {
		t.Fatal("receipt changed despite rolled-back transaction")
	}
}

func TestRepositorySyncRollsBackCreatedOwnedArtifact(t *testing.T) {
	repo, receipt := initializeSyncFixture(t, ProfileAdvanced)
	managed := receipt.ManagedFiles[firstHarnessManagedFile(t, receipt)]
	target := filepath.Join(repo, filepath.FromSlash(managed.Path))
	if err := os.Remove(target); err != nil {
		t.Fatal(err)
	}
	plan, err := BuildSyncPlan(repo, syncTestVersion)
	if err != nil {
		t.Fatal(err)
	}
	report, err := applySyncPlan(plan, plan.PlanDigest, syncTestVersion, syncApplyOptions{failAfter: 1})
	if err == nil || report.Status != SyncRolledBack {
		t.Fatalf("created-artifact rollback = %+v err=%v", report, err)
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatalf("created artifact survived rollback: %v", err)
	}
}

func TestRepositorySyncImportsOneLegacyReceipt(t *testing.T) {
	repo, _ := initializeSyncFixture(t, ProfileGoverned)
	if err := os.Remove(filepath.Join(repo, filepath.FromSlash(RepositoryReceiptRelativePath))); err != nil {
		t.Fatal(err)
	}
	plan, err := BuildSyncPlan(repo, syncTestVersion)
	if err != nil {
		t.Fatal(err)
	}
	if !plan.LegacyReceiptImport {
		t.Fatal("legacy bootstrap receipt was not identified")
	}
	report, err := ApplySyncPlan(plan, plan.PlanDigest, syncTestVersion)
	if err != nil || report.Status != SyncComplete {
		t.Fatalf("legacy import = %+v err=%v", report, err)
	}
	if _, err := LoadRepositoryReceipt(repo); err != nil {
		t.Fatal(err)
	}
}

func TestRepositorySyncImportsHistoricalAdvancedReceipt(t *testing.T) {
	repo, receipt := initializeSyncFixture(t, ProfileAdvanced)
	currentPlanPath := filepath.ToSlash(filepath.Join(".reconc", "bootstrap-plan-"+receipt.PlanDigest+".json"))
	currentPlan, err := LoadPlan(filepath.Join(repo, filepath.FromSlash(currentPlanPath)))
	if err != nil {
		t.Fatalf("load current bootstrap plan: %v", err)
	}
	privateReceipt, currentReceiptPath, err := loadInstallReceipt(currentPlan)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(repo, filepath.FromSlash(RepositoryReceiptRelativePath))); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(repo, filepath.FromSlash(recordedPlanPath(currentPlan)))); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(repo, filepath.FromSlash(currentReceiptPath))); err != nil {
		t.Fatal(err)
	}

	legacy := *currentPlan
	legacy.ProductVersion = "0.8.8"
	legacy.Selection.HarnessPacks = append([]HarnessPackSelection{}, currentPlan.Selection.HarnessPacks...)
	legacy.Selection.HarnessPacks[0].Version = "0.8.8"
	legacy.Selection.HarnessPacks[0].Digest = strings.Repeat("a", 64)
	legacy.PlanDigest = ""
	legacy.PlanDigest, err = computePlanDigest(&legacy)
	if err != nil {
		t.Fatal(err)
	}
	legacyBody, err := encodePlan(&legacy)
	if err != nil {
		t.Fatal(err)
	}
	legacyPlanPath := recordedPlanPath(&legacy)
	if err := os.WriteFile(filepath.Join(repo, filepath.FromSlash(legacyPlanPath)), legacyBody, 0o600); err != nil {
		t.Fatal(err)
	}

	privateReceipt.ProductVersion = legacy.ProductVersion
	privateReceipt.PlanDigest = legacy.PlanDigest
	privateReceipt.HarnessPacks = append([]HarnessPackSelection{}, legacy.Selection.HarnessPacks...)
	for index := range privateReceipt.Entries {
		if privateReceipt.Entries[index].Path == recordedPlanPath(currentPlan) {
			privateReceipt.Entries[index].Path = legacyPlanPath
			privateReceipt.Entries[index].SHA256 = bytesSHA256(legacyBody)
		}
	}
	privateReceipt.Digest = ""
	privateReceipt.Digest, err = computeInstallReceiptDigest(privateReceipt)
	if err != nil {
		t.Fatal(err)
	}
	privateBody, err := json.MarshalIndent(privateReceipt, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	privateBody = append(privateBody, '\n')
	if err := os.WriteFile(
		filepath.Join(repo, filepath.FromSlash(installReceiptPath(legacy.PlanDigest))),
		privateBody,
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	loadedLegacy, err := LoadPlan(filepath.Join(repo, filepath.FromSlash(legacyPlanPath)))
	if err != nil {
		t.Fatalf("load historical bootstrap plan: %v", err)
	}
	if _, _, err := loadInstallReceipt(loadedLegacy); err != nil {
		t.Fatalf("load historical bootstrap receipt: %v", err)
	}

	plan, err := BuildSyncPlan(repo, syncTestVersion)
	if err != nil {
		t.Fatal(err)
	}
	if !plan.LegacyReceiptImport || plan.CurrentProductVersion != "0.8.8" {
		t.Fatalf("historical advanced migration plan = %+v", plan)
	}
	report, err := ApplySyncPlan(plan, plan.PlanDigest, syncTestVersion)
	if err != nil || report.Status != SyncComplete {
		t.Fatalf("historical advanced apply = %+v err=%v", report, err)
	}
	updated, err := LoadRepositoryReceipt(repo)
	if err != nil {
		t.Fatal(err)
	}
	if updated.ProductVersion != syncTestVersion ||
		len(updated.HarnessPacks) != 1 ||
		updated.HarnessPacks[0].Digest == strings.Repeat("a", 64) {
		t.Fatalf("historical advanced receipt was not migrated: %+v", updated)
	}
}

func TestRepositorySyncSerializesCompetingApply(t *testing.T) {
	repo, receipt := initializeSyncFixture(t, ProfileGoverned)
	rewriteTestReceipt(t, repo, receipt, "0.8.8")
	plan, err := BuildSyncPlan(repo, syncTestVersion)
	if err != nil {
		t.Fatal(err)
	}
	type result struct {
		report *SyncReport
		err    error
	}
	results := make(chan result, 2)
	for range 2 {
		go func() {
			report, applyErr := ApplySyncPlan(plan, plan.PlanDigest, syncTestVersion)
			results <- result{report: report, err: applyErr}
		}()
	}
	complete := 0
	refused := 0
	for range 2 {
		got := <-results
		switch {
		case got.err == nil && got.report.Status == SyncComplete:
			complete++
		case got.err != nil && got.report.Status == SyncRefused:
			refused++
		default:
			t.Fatalf("unexpected competing apply result: %+v err=%v", got.report, got.err)
		}
	}
	if complete != 1 || refused != 1 {
		t.Fatalf("competing apply outcomes: complete=%d refused=%d", complete, refused)
	}
}

func TestRepositorySyncMigratesOnlyReceiptOwnedPolicyLock(t *testing.T) {
	repo, receipt := initializeSyncFixture(t, ProfileGoverned)
	generatedIndex := generatedArtifactIndex(t, receipt, ".reconc/policy.lock.json")
	lockPath := filepath.Join(repo, ".reconc", "policy.lock.json")
	body, err := os.ReadFile(lockPath)
	if err != nil {
		t.Fatal(err)
	}
	var payload map[string]interface{}
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatal(err)
	}
	payload["$schema"] = compiler.LegacyLockfileSchemaV1
	payload["format_version"] = "1"
	payload["repo_root"] = repo
	discovery := payload["discovery"].(map[string]interface{})
	discovery["repo_root"] = repo
	discovery["start_path"] = repo
	digest, err := compiler.ComputeLockDigest(payload)
	if err != nil {
		t.Fatal(err)
	}
	payload["lock_digest"] = digest
	legacy, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	legacy = append(legacy, '\n')
	if err := os.WriteFile(lockPath, legacy, 0o644); err != nil {
		t.Fatal(err)
	}
	receipt.GeneratedArtifacts[generatedIndex].SHA256 = bytesSHA256(legacy)
	rewriteTestReceipt(t, repo, receipt, "0.8.8")

	plan, err := BuildSyncPlan(repo, syncTestVersion)
	if err != nil {
		t.Fatal(err)
	}
	action := syncActionForPath(t, plan, ".reconc/policy.lock.json")
	if action.State != SyncReplaceOwned || len(plan.Migrations) != 1 {
		t.Fatalf("policy migration action = %+v migrations=%v", action, plan.Migrations)
	}
	if _, err := ApplySyncPlan(plan, plan.PlanDigest, syncTestVersion); err != nil {
		t.Fatal(err)
	}
	if err := reconruntimeValidatePolicy(t, repo); err != nil {
		t.Fatal(err)
	}
}

func TestRepositorySyncClassifiesMissingGeneratedPolicyLock(t *testing.T) {
	repo, _ := initializeSyncFixture(t, ProfileGoverned)
	if err := os.Remove(filepath.Join(repo, ".reconc", "policy.lock.json")); err != nil {
		t.Fatal(err)
	}
	plan, err := BuildSyncPlan(repo, syncTestVersion)
	if err != nil {
		t.Fatal(err)
	}
	action := syncActionForPath(t, plan, ".reconc/policy.lock.json")
	if action.State != SyncManualReview ||
		!strings.Contains(action.Reason, "reconc compile") ||
		len(plan.BlockingIssues) == 0 {
		t.Fatalf("missing generated policy lock action = %+v issues=%v", action, plan.BlockingIssues)
	}
}

func TestRepositorySyncClassifiesIncompleteAndLegacyStates(t *testing.T) {
	t.Run("missing owned file", func(t *testing.T) {
		repo, receipt := initializeSyncFixture(t, ProfileAdvanced)
		managed := receipt.ManagedFiles[firstHarnessManagedFile(t, receipt)]
		target := filepath.Join(repo, filepath.FromSlash(managed.Path))
		if err := os.Remove(target); err != nil {
			t.Fatal(err)
		}
		plan, err := BuildSyncPlan(repo, syncTestVersion)
		if err != nil {
			t.Fatal(err)
		}
		if action := syncActionForPath(t, plan, managed.Path); action.State != SyncCreateOwned {
			t.Fatalf("missing owned action = %+v", action)
		}
		report, err := ApplySyncPlan(plan, plan.PlanDigest, syncTestVersion)
		if err != nil || report.Status != SyncComplete {
			t.Fatalf("restore missing owned file = %+v err=%v", report, err)
		}
		if _, err := os.Stat(target); err != nil {
			t.Fatalf("restored owned file: %v", err)
		}
	})
	t.Run("orphaned legacy file", func(t *testing.T) {
		repo, receipt := initializeSyncFixture(t, ProfileGoverned)
		relative := ".reconc/legacy-owned.txt"
		body := []byte("legacy\n")
		if err := os.WriteFile(filepath.Join(repo, filepath.FromSlash(relative)), body, 0o644); err != nil {
			t.Fatal(err)
		}
		receipt.ManagedFiles = append(receipt.ManagedFiles, ManagedFile{
			Path: relative, Mode: 0o644, SHA256: bytesSHA256(body),
			Component: "hook:legacy", Ownership: "file",
		})
		normalizeRepositoryReceipt(receipt)
		rewriteTestReceipt(t, repo, receipt, "0.8.8")
		plan, err := BuildSyncPlan(repo, syncTestVersion)
		if err != nil {
			t.Fatal(err)
		}
		if action := syncActionForPath(t, plan, relative); action.State != SyncOrphanedLegacy ||
			len(plan.BlockingIssues) == 0 {
			t.Fatalf("orphaned legacy action = %+v issues=%v", action, plan.BlockingIssues)
		}
	})
	t.Run("incompatible policy lock", func(t *testing.T) {
		repo, receipt := initializeSyncFixture(t, ProfileGoverned)
		lockPath := filepath.Join(repo, ".reconc", "policy.lock.json")
		body := []byte("{\"format_version\":\"unsupported\"}\n")
		if err := os.WriteFile(lockPath, body, 0o644); err != nil {
			t.Fatal(err)
		}
		index := generatedArtifactIndex(t, receipt, ".reconc/policy.lock.json")
		receipt.GeneratedArtifacts[index].SHA256 = bytesSHA256(body)
		rewriteTestReceipt(t, repo, receipt, "0.8.8")
		plan, err := BuildSyncPlan(repo, syncTestVersion)
		if err != nil {
			t.Fatal(err)
		}
		if action := syncActionForPath(t, plan, ".reconc/policy.lock.json"); action.State != SyncIncompatible ||
			len(plan.BlockingIssues) == 0 {
			t.Fatalf("incompatible policy action = %+v issues=%v", action, plan.BlockingIssues)
		}
	})
}

func TestRepositorySyncClassifiesTamperedTargets(t *testing.T) {
	t.Run("non-regular owned target", func(t *testing.T) {
		root := t.TempDir()
		if err := os.Mkdir(filepath.Join(root, "owned"), 0o755); err != nil {
			t.Fatal(err)
		}
		action, err := planSyncArtifact(root,
			desiredArtifact{component: "hook:test", path: "owned", mode: 0o644, content: []byte("new\n")},
			ManagedFile{Path: "owned", Mode: 0o644, SHA256: strings.Repeat("a", 64)},
			ManagedBlock{},
		)
		if err != nil || action.State != SyncUserDrift {
			t.Fatalf("non-regular action = %+v err=%v", action, err)
		}
	})
	t.Run("malformed managed block", func(t *testing.T) {
		root := t.TempDir()
		if err := os.WriteFile(filepath.Join(root, ".gitignore"), []byte("no markers\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		action, err := planSyncArtifact(root,
			desiredArtifact{component: "ignore-policy", path: ".gitignore", mode: 0o644, content: []byte("desired\n")},
			ManagedFile{},
			ManagedBlock{
				Path: ".gitignore", BlockStart: "start", BlockEnd: "end",
				ManagedSHA256: strings.Repeat("a", 64),
			},
		)
		if err != nil || action.State != SyncManualReview {
			t.Fatalf("malformed block action = %+v err=%v", action, err)
		}
	})
	t.Run("drifted managed block", func(t *testing.T) {
		root := t.TempDir()
		body := []byte("start\ncurrent\nend\n")
		if err := os.WriteFile(filepath.Join(root, ".gitignore"), body, 0o644); err != nil {
			t.Fatal(err)
		}
		action, err := planSyncArtifact(root,
			desiredArtifact{component: "ignore-policy", path: ".gitignore", mode: 0o644, content: []byte("start\ndesired\nend\n")},
			ManagedFile{},
			ManagedBlock{
				Path: ".gitignore", BlockStart: "start", BlockEnd: "end",
				ManagedSHA256: strings.Repeat("a", 64),
			},
		)
		if err != nil || action.State != SyncUserDrift {
			t.Fatalf("drifted block action = %+v err=%v", action, err)
		}
	})
	t.Run("unowned existing target", func(t *testing.T) {
		root := t.TempDir()
		if err := os.WriteFile(filepath.Join(root, "target"), []byte("user\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		action, err := planSyncArtifact(root,
			desiredArtifact{component: "policy", path: "target", mode: 0o644, content: []byte("desired\n")},
			ManagedFile{},
			ManagedBlock{},
		)
		if err != nil || action.State != SyncManualReview {
			t.Fatalf("unowned action = %+v err=%v", action, err)
		}
	})
	t.Run("non-regular generated lock", func(t *testing.T) {
		root := t.TempDir()
		if err := os.MkdirAll(filepath.Join(root, ".reconc"), 0o755); err != nil {
			t.Fatal(err)
		}
		target := filepath.Join(root, "target")
		if err := os.WriteFile(target, []byte("{}\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		lock := filepath.Join(root, ".reconc", "policy.lock.json")
		if err := os.Symlink(target, lock); err != nil {
			t.Skipf("symlink unavailable: %v", err)
		}
		action, _, err := planPolicyLockMigration(root, &RepositoryReceipt{
			GeneratedArtifacts: []GeneratedArtifact{{
				Path: ".reconc/policy.lock.json", SHA256: strings.Repeat("a", 64),
			}},
		})
		if err != nil || action.State != SyncUserDrift {
			t.Fatalf("non-regular lock action = %+v err=%v", action, err)
		}
	})
	t.Run("drifted generated lock", func(t *testing.T) {
		root := t.TempDir()
		if err := os.MkdirAll(filepath.Join(root, ".reconc"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, ".reconc", "policy.lock.json"), []byte("{}\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		action, _, err := planPolicyLockMigration(root, &RepositoryReceipt{
			GeneratedArtifacts: []GeneratedArtifact{{
				Path: ".reconc/policy.lock.json", SHA256: strings.Repeat("a", 64),
			}},
		})
		if err != nil || action.State != SyncUserDrift {
			t.Fatalf("drifted lock action = %+v err=%v", action, err)
		}
	})
}

func TestRepositorySyncByteSourcesAndConcurrentRollback(t *testing.T) {
	root := t.TempDir()
	if _, err := desiredSyncBytes(root, SyncAction{Path: "missing"}, desiredArtifact{}, false); err == nil {
		t.Fatal("missing desired artifact was accepted")
	}
	if _, err := desiredSyncBytes(root, SyncAction{}, desiredArtifact{sourcePath: filepath.Join(root, "missing")}, true); err == nil {
		t.Fatal("missing binary source was accepted")
	}
	if _, err := desiredSyncBytes(root, SyncAction{}, desiredArtifact{sourcePath: root}, true); err == nil {
		t.Fatal("directory binary source was accepted")
	}
	source := filepath.Join(root, "binary")
	if err := os.WriteFile(source, []byte("binary\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	body, err := desiredSyncBytes(root, SyncAction{}, desiredArtifact{sourcePath: source}, true)
	if err != nil || string(body) != "binary\n" {
		t.Fatalf("binary bytes = %q err=%v", body, err)
	}

	created := filepath.Join(root, "created")
	if err := os.WriteFile(created, []byte("external\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	rolledBack, err := rollbackSyncChanges(root, []syncBackup{{
		path: created, created: true, expectedAfter: bytesSHA256([]byte("expected\n")),
	}}, nil, nil)
	if err == nil || len(rolledBack) != 0 || !strings.Contains(err.Error(), "concurrent change") {
		t.Fatalf("concurrent rollback = %v err=%v", rolledBack, err)
	}
	current, err := os.ReadFile(created)
	if err != nil || string(current) != "external\n" {
		t.Fatalf("concurrent rollback changed external bytes: %q err=%v", current, err)
	}
}

func TestRepositoryReceiptRejectsInvalidOwnershipContracts(t *testing.T) {
	_, valid := initializeSyncFixture(t, ProfileGoverned)
	for _, test := range []struct {
		name   string
		mutate func(*RepositoryReceipt)
	}{
		{name: "schema", mutate: func(receipt *RepositoryReceipt) {
			receipt.Schema = "https://invalid.example/schema.json"
		}},
		{name: "product", mutate: func(receipt *RepositoryReceipt) {
			receipt.ProductVersion = ""
		}},
		{name: "generation", mutate: func(receipt *RepositoryReceipt) {
			receipt.Generation = 0
		}},
		{name: "policy pack", mutate: func(receipt *RepositoryReceipt) {
			receipt.PolicyPacks = append(receipt.PolicyPacks, receipt.PolicyPacks[0])
		}},
		{name: "harness pack", mutate: func(receipt *RepositoryReceipt) {
			receipt.HarnessPacks = []HarnessPackIdentity{{Name: "advanced"}}
		}},
		{name: "hooks", mutate: func(receipt *RepositoryReceipt) {
			receipt.Hooks = []string{"z", "a"}
		}},
		{name: "policy source", mutate: func(receipt *RepositoryReceipt) {
			receipt.PolicySources = []string{"../escape"}
		}},
		{name: "managed file", mutate: func(receipt *RepositoryReceipt) {
			receipt.ManagedFiles = []ManagedFile{{
				Path: "../escape", Mode: 0o644, SHA256: strings.Repeat("a", 64),
				Component: "hook:test", Ownership: "file",
			}}
		}},
		{name: "managed block conflict", mutate: func(receipt *RepositoryReceipt) {
			receipt.ManagedBlocks = append(receipt.ManagedBlocks, receipt.ManagedBlocks[0])
		}},
		{name: "generated conflict", mutate: func(receipt *RepositoryReceipt) {
			receipt.GeneratedArtifacts = append(receipt.GeneratedArtifacts, receipt.GeneratedArtifacts[0])
		}},
		{name: "user ownership conflict", mutate: func(receipt *RepositoryReceipt) {
			receipt.UserOwnedPaths = append(receipt.UserOwnedPaths, receipt.ManagedBlocks[0].Path)
			sort.Strings(receipt.UserOwnedPaths)
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			receipt := cloneRepositoryReceipt(t, valid)
			test.mutate(receipt)
			if err := ValidateRepositoryReceipt(receipt); err == nil {
				t.Fatalf("invalid %s receipt was accepted: %+v", test.name, receipt)
			}
		})
	}
}

func TestRepositoryReceiptBuildersRequireTransactionEvidence(t *testing.T) {
	repo, receipt := initializeSyncFixture(t, ProfileGoverned)
	planPath := filepath.Join(repo, ".reconc", "bootstrap-plan-"+receipt.PlanDigest+".json")
	plan, err := LoadPlan(planPath)
	if err != nil {
		t.Fatal(err)
	}
	privateReceipt, _, err := loadInstallReceipt(plan)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := BuildRepositoryReceipt(nil, nil, 1, strings.Repeat("a", 64)); err == nil {
		t.Fatal("nil bootstrap plan was accepted")
	}
	if _, err := BuildRepositoryReceipt(plan, nil, 1, plan.PlanDigest); err == nil {
		t.Fatal("missing private receipt was accepted")
	}
	if _, err := BuildRepositoryReceipt(plan, privateReceipt, 0, plan.PlanDigest); err == nil {
		t.Fatal("zero receipt generation was accepted")
	}
	if _, err := computeRepositoryReceiptDigest(nil); err == nil {
		t.Fatal("nil repository receipt digest was accepted")
	}
	invalid := cloneRepositoryReceipt(t, receipt)
	invalid.ProductVersion = ""
	if _, err := encodeRepositoryReceipt(invalid); err == nil {
		t.Fatal("invalid repository receipt was encoded")
	}
}

func TestRepositoryReceiptLoaderRejectsUnknownTrailingAndSymlinkInput(t *testing.T) {
	t.Run("unknown field", func(t *testing.T) {
		repo, _ := initializeSyncFixture(t, ProfileGoverned)
		path := filepath.Join(repo, filepath.FromSlash(RepositoryReceiptRelativePath))
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var document map[string]interface{}
		if err := json.Unmarshal(body, &document); err != nil {
			t.Fatal(err)
		}
		document["unknown"] = true
		body, err = json.Marshal(document)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, body, 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := LoadRepositoryReceipt(repo); err == nil || !strings.Contains(err.Error(), "unknown field") {
			t.Fatalf("unknown field error = %v", err)
		}
	})
	t.Run("trailing document", func(t *testing.T) {
		repo, _ := initializeSyncFixture(t, ProfileGoverned)
		path := filepath.Join(repo, filepath.FromSlash(RepositoryReceiptRelativePath))
		file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := file.WriteString("{}\n"); err != nil {
			_ = file.Close()
			t.Fatal(err)
		}
		if err := file.Close(); err != nil {
			t.Fatal(err)
		}
		if _, err := LoadRepositoryReceipt(repo); err == nil || !strings.Contains(err.Error(), "exactly one") {
			t.Fatalf("trailing document error = %v", err)
		}
	})
	t.Run("symlink", func(t *testing.T) {
		repo, _ := initializeSyncFixture(t, ProfileGoverned)
		path := filepath.Join(repo, filepath.FromSlash(RepositoryReceiptRelativePath))
		target := filepath.Join(repo, ".reconc", "receipt-target.json")
		if err := os.Rename(path, target); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(target, path); err != nil {
			t.Skipf("symlink unavailable: %v", err)
		}
		if _, err := LoadRepositoryReceipt(repo); err == nil || !strings.Contains(err.Error(), "real regular file") {
			t.Fatalf("symlink receipt error = %v", err)
		}
	})
}

func TestSyncPlanFilesRefuseSymlinks(t *testing.T) {
	repo, _ := initializeSyncFixture(t, ProfileGoverned)
	plan, err := BuildSyncPlan(repo, syncTestVersion)
	if err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "target.json")
	if err := os.WriteFile(target, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(t.TempDir(), "plan.json")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if _, err := WriteSyncPlan(link, plan); err == nil {
		t.Fatal("write accepted a symlink output")
	}
	if _, err := ReplaceSyncPlan(link, plan); err == nil {
		t.Fatal("replace accepted a symlink output")
	}
	if _, err := LoadSyncPlan(link); err == nil {
		t.Fatal("load accepted a symlink plan")
	}
}

func TestSyncPlanLoaderRejectsMalformedInputs(t *testing.T) {
	repo, _ := initializeSyncFixture(t, ProfileGoverned)
	plan, err := BuildSyncPlan(repo, syncTestVersion)
	if err != nil {
		t.Fatal(err)
	}
	validBody, err := encodeSyncPlan(plan)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name string
		body []byte
		want string
	}{
		{name: "empty", body: []byte{}, want: "bounded regular file"},
		{name: "invalid JSON", body: []byte("{"), want: "decode"},
		{name: "trailing document", body: append(append([]byte{}, validBody...), []byte("{}\n")...), want: "exactly one"},
		{name: "unknown field", body: []byte(strings.Replace(
			string(validBody),
			"\"format_version\"",
			"\"unknown\": true,\n  \"format_version\"",
			1,
		)), want: "unknown field"},
	} {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "plan.json")
			if err := os.WriteFile(path, test.body, 0o644); err != nil {
				t.Fatal(err)
			}
			if _, err := LoadSyncPlan(path); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("malformed plan error = %v", err)
			}
		})
	}
	if _, err := LoadSyncPlan(filepath.Join(t.TempDir(), "missing.json")); err == nil ||
		!strings.Contains(err.Error(), "inspect") {
		t.Fatalf("missing plan error = %v", err)
	}
}

func TestSyncPlanOutputLifecycle(t *testing.T) {
	repo, receipt := initializeSyncFixture(t, ProfileGoverned)
	plan, err := BuildSyncPlan(repo, syncTestVersion)
	if err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(t.TempDir(), "sync-plan.json")
	if state, err := WriteSyncPlan(output, plan); err != nil || state != "created" {
		t.Fatalf("create output: state=%s err=%v", state, err)
	}
	if state, err := WriteSyncPlan(output, plan); err != nil || state != "unchanged" {
		t.Fatalf("idempotent output: state=%s err=%v", state, err)
	}
	rewriteTestReceipt(t, repo, receipt, "0.8.8")
	updated, err := BuildSyncPlan(repo, syncTestVersion)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := WriteSyncPlan(output, updated); err == nil {
		t.Fatal("write replaced different plan without explicit replacement")
	}
	if state, err := ReplaceSyncPlan(output, updated); err != nil || state != "replaced" {
		t.Fatalf("replace output: state=%s err=%v", state, err)
	}
	if state, err := ReplaceSyncPlan(output, updated); err != nil || state != "unchanged" {
		t.Fatalf("idempotent replace output: state=%s err=%v", state, err)
	}
	loaded, err := LoadSyncPlan(output)
	if err != nil || loaded.PlanDigest != updated.PlanDigest {
		t.Fatalf("load replaced output = %+v err=%v", loaded, err)
	}

	otherRepo, _ := initializeSyncFixture(t, ProfileGoverned)
	other, err := BuildSyncPlan(otherRepo, syncTestVersion)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ReplaceSyncPlan(output, other); err == nil {
		t.Fatal("replacement accepted a plan for another repository")
	}
}

func TestRepositorySyncRefusesUnboundApply(t *testing.T) {
	repo, receipt := initializeSyncFixture(t, ProfileGoverned)
	rewriteTestReceipt(t, repo, receipt, "0.8.8")
	plan, err := BuildSyncPlan(repo, syncTestVersion)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name    string
		digest  string
		version string
	}{
		{name: "digest", digest: strings.Repeat("0", 64), version: syncTestVersion},
		{name: "version", digest: plan.PlanDigest, version: "9.9.9"},
	} {
		t.Run(test.name, func(t *testing.T) {
			report, err := ApplySyncPlan(plan, test.digest, test.version)
			if err == nil || report.Status != SyncRefused || len(report.Changed) != 0 {
				t.Fatalf("unbound apply = %+v err=%v", report, err)
			}
		})
	}
}

func TestSyncPlanRejectsInvalidContracts(t *testing.T) {
	repo, _ := initializeSyncFixture(t, ProfileAdvanced)
	valid, err := BuildSyncPlan(repo, syncTestVersion)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name   string
		want   string
		mutate func(*SyncPlan)
	}{
		{name: "schema", want: "unsupported", mutate: func(plan *SyncPlan) {
			plan.Schema = "https://invalid.example/schema.json"
		}},
		{name: "identity", want: "identity", mutate: func(plan *SyncPlan) {
			plan.CurrentReceiptDigest = "bad"
		}},
		{name: "Git snapshot", want: "different repository", mutate: func(plan *SyncPlan) {
			plan.GitSnapshot = &commandproof.Snapshot{RepoRoot: filepath.Dir(plan.RepoRoot)}
		}},
		{name: "policy pack", want: "policy pack", mutate: func(plan *SyncPlan) {
			plan.TargetPolicyPacks = append(plan.TargetPolicyPacks, plan.TargetPolicyPacks[0])
		}},
		{name: "harness pack", want: "harness pack", mutate: func(plan *SyncPlan) {
			plan.TargetHarnessPacks[0].Digest = "bad"
		}},
		{name: "action mode", want: "action 0 is invalid", mutate: func(plan *SyncPlan) {
			plan.Actions[0].Mode = 0
		}},
		{name: "action order", want: "uniquely sorted", mutate: func(plan *SyncPlan) {
			plan.Actions = append(plan.Actions, plan.Actions[0])
		}},
		{name: "action digest", want: "invalid digest", mutate: func(plan *SyncPlan) {
			plan.Actions[0].CurrentSHA256 = "bad"
		}},
		{name: "candidate", want: "invalid candidate", mutate: func(plan *SyncPlan) {
			plan.Actions[0].CandidatePath = "../escape"
		}},
		{name: "candidate order", want: "uniquely sorted", mutate: func(plan *SyncPlan) {
			plan.Candidates = []string{"z", "a"}
		}},
		{name: "blocking order", want: "must be sorted", mutate: func(plan *SyncPlan) {
			plan.BlockingIssues = []string{"z", "a"}
		}},
		{name: "digest", want: "digest mismatch", mutate: func(plan *SyncPlan) {
			plan.TargetProductVersion = "9.9.9"
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			plan := cloneSyncPlan(t, valid)
			test.mutate(plan)
			err := ValidateSyncPlan(plan)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("invalid %s plan error = %v", test.name, err)
			}
		})
	}
}

func TestSyncPlanRejectsTraversalEvenWithRecomputedDigest(t *testing.T) {
	repo, _ := initializeSyncFixture(t, ProfileGoverned)
	plan, err := BuildSyncPlan(repo, syncTestVersion)
	if err != nil {
		t.Fatal(err)
	}
	plan.Actions[0].Path = "../escape"
	plan.PlanDigest, err = computeSyncPlanDigest(plan)
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateSyncPlan(plan); err == nil {
		t.Fatal("traversal action was accepted")
	}
}

func initializeSyncFixture(t *testing.T, profile ProfileName) (string, *RepositoryReceipt) {
	t.Helper()
	repo := t.TempDir()
	t.Setenv("RECONC_HOME", t.TempDir())
	report, err := Initialize(InitRequest{
		RepoRoot: repo, Profile: profile, NoHooks: true,
	}, syncTestVersion)
	if err != nil || report.Status != InitComplete {
		t.Fatalf("initialize sync fixture: %+v err=%v", report, err)
	}
	receipt, err := LoadRepositoryReceipt(repo)
	if err != nil {
		t.Fatal(err)
	}
	return repo, receipt
}

func rewriteTestReceipt(t *testing.T, repo string, receipt *RepositoryReceipt, productVersion string) {
	t.Helper()
	receipt.ProductVersion = productVersion
	receipt.ReceiptDigest = ""
	digest, err := computeRepositoryReceiptDigest(receipt)
	if err != nil {
		t.Fatal(err)
	}
	receipt.ReceiptDigest = digest
	if _, err := writeRepositoryReceiptAtomic(repo, receipt); err != nil {
		t.Fatal(err)
	}
}

func cloneRepositoryReceipt(t *testing.T, receipt *RepositoryReceipt) *RepositoryReceipt {
	t.Helper()
	body, err := json.Marshal(receipt)
	if err != nil {
		t.Fatal(err)
	}
	var clone RepositoryReceipt
	if err := json.Unmarshal(body, &clone); err != nil {
		t.Fatal(err)
	}
	return &clone
}

func cloneSyncPlan(t *testing.T, plan *SyncPlan) *SyncPlan {
	t.Helper()
	body, err := json.Marshal(plan)
	if err != nil {
		t.Fatal(err)
	}
	var clone SyncPlan
	if err := json.Unmarshal(body, &clone); err != nil {
		t.Fatal(err)
	}
	return &clone
}

func firstHarnessManagedFile(t *testing.T, receipt *RepositoryReceipt) int {
	t.Helper()
	for index, file := range receipt.ManagedFiles {
		if strings.HasPrefix(file.Component, "harness-pack:") {
			return index
		}
	}
	t.Fatal("fixture has no harness managed file")
	return -1
}

func managedBlockIndex(t *testing.T, receipt *RepositoryReceipt, path string) int {
	t.Helper()
	for index, block := range receipt.ManagedBlocks {
		if block.Path == path {
			return index
		}
	}
	t.Fatalf("fixture has no managed block %s", path)
	return -1
}

func generatedArtifactIndex(t *testing.T, receipt *RepositoryReceipt, path string) int {
	t.Helper()
	for index, artifact := range receipt.GeneratedArtifacts {
		if artifact.Path == path {
			return index
		}
	}
	t.Fatalf("fixture has no generated artifact %s", path)
	return -1
}

func syncActionForPath(t *testing.T, plan *SyncPlan, path string) SyncAction {
	t.Helper()
	for _, action := range plan.Actions {
		if action.Path == path {
			return action
		}
	}
	t.Fatalf("plan has no action for %s", path)
	return SyncAction{}
}

func reconruntimeValidatePolicy(t *testing.T, repo string) error {
	t.Helper()
	verification, err := VerifyRepository(repo, syncTestVersion)
	if err != nil {
		return err
	}
	if !verification.Valid {
		return &testVerificationError{message: verification.NextAction}
	}
	return nil
}

type testVerificationError struct {
	message string
}

func (err *testVerificationError) Error() string {
	return err.message
}

func TestRepositorySyncRejectsMalformedHelperInputs(t *testing.T) {
	for _, body := range [][]byte{
		[]byte("{"),
		[]byte("{}{}"),
	} {
		if _, _, err := migratePolicyLockBytes(body); err == nil {
			t.Fatalf("malformed policy lock %q was accepted", body)
		}
	}
	if _, err := computeSyncPlanDigest(nil); err == nil {
		t.Fatal("nil sync plan digest was accepted")
	}
	if _, err := encodeSyncPlan(nil); err == nil {
		t.Fatal("nil sync plan was encoded")
	}
	if validSyncState(SyncActionState("unknown")) {
		t.Fatal("unknown sync action state was accepted")
	}
	candidate := syncCandidatePath(SyncAction{Path: "owned.txt", DesiredSHA256: "short"})
	if candidate != "owned.txt.reconc-sync-candidate-000000000000" {
		t.Fatalf("short-digest candidate = %q", candidate)
	}
}
