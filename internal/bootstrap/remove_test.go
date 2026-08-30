package bootstrap

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"reconc.dev/reconc/internal/repositoryignore"
)

func TestBootstrapRemoveReversesOnlyReceiptOwnedArtifacts(t *testing.T) {
	bootstrapTestHome(t)
	repo := t.TempDir()
	plan, err := BuildPlan(Request{RepoRoot: repo, Profile: ProfileMinimal}, "test-version")
	if err != nil {
		t.Fatal(err)
	}
	report, err := Apply(plan, "test-version")
	if err != nil {
		t.Fatal(err)
	}
	if report.ReceiptPath == "" {
		t.Fatal("bootstrap apply did not create an install receipt")
	}
	userPath := filepath.Join(repo, "user-owned.txt")
	if err := os.WriteFile(userPath, []byte("keep\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	removal, err := Remove(plan)
	if err != nil {
		t.Fatal(err)
	}
	if removal.Status != RemovalComplete {
		t.Fatalf("removal = %+v", removal)
	}
	for _, relative := range []string{".reconc/policy.lock.json", RepositoryReceiptRelativePath, report.ReceiptPath} {
		if _, err := os.Stat(filepath.Join(repo, filepath.FromSlash(relative))); !os.IsNotExist(err) {
			t.Fatalf("receipt-owned path still exists: %s (%v)", relative, err)
		}
	}
	for _, relative := range []string{".gitignore", ".reconc.yml", "AGENTS.md"} {
		if _, err := os.Stat(filepath.Join(repo, filepath.FromSlash(relative))); err != nil {
			t.Fatalf("portable user-owned or managed-block path was removed: %s (%v)", relative, err)
		}
	}
	if body, err := os.ReadFile(userPath); err != nil || string(body) != "keep\n" {
		t.Fatalf("user file changed: body=%q err=%v", body, err)
	}
}

func TestBootstrapRemoveConvergesWhenOwnedEntryIsAlreadyAbsent(t *testing.T) {
	bootstrapTestHome(t)
	repo := t.TempDir()
	plan, err := BuildPlan(Request{RepoRoot: repo, Profile: ProfileMinimal}, "test-version")
	if err != nil {
		t.Fatal(err)
	}
	report, err := Apply(plan, "test-version")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(repo, ".reconc.yml")); err != nil {
		t.Fatal(err)
	}

	removal, err := Remove(plan)
	if err != nil {
		t.Fatal(err)
	}
	if removal.Status != RemovalComplete || len(removal.Preserved) != 0 {
		t.Fatalf("already-absent removal = %+v", removal)
	}
	if _, err := os.Stat(filepath.Join(repo, filepath.FromSlash(report.ReceiptPath))); !os.IsNotExist(err) {
		t.Fatalf("completed removal retained receipt: %v", err)
	}
}

func TestBootstrapRemoveConvergesWhenManagedBlockIsAlreadyAbsent(t *testing.T) {
	bootstrapTestHome(t)
	repo := t.TempDir()
	plan, err := BuildPlan(Request{RepoRoot: repo, Profile: ProfileMinimal}, "test-version")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Apply(plan, "test-version"); err != nil {
		t.Fatal(err)
	}
	agentsPath := filepath.Join(repo, "AGENTS.md")
	body, err := os.ReadFile(agentsPath)
	if err != nil {
		t.Fatal(err)
	}
	stripped, found, err := stripReceiptManagedBlock(string(body), agentBlockStart, agentBlockEnd)
	if err != nil || !found {
		t.Fatalf("strip managed block: found=%v err=%v", found, err)
	}
	if err := os.WriteFile(agentsPath, []byte(stripped+"User-owned addition.\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	removal, err := Remove(plan)
	if err != nil {
		t.Fatal(err)
	}
	if removal.Status != RemovalComplete || len(removal.Preserved) != 0 {
		t.Fatalf("already-removed managed block = %+v", removal)
	}
	current, err := os.ReadFile(agentsPath)
	if err != nil || !strings.Contains(string(current), "User-owned addition.") {
		t.Fatalf("user-owned content changed: body=%q err=%v", current, err)
	}
}

func TestBootstrapRemovePreservesOutsideManagedBlockBytes(t *testing.T) {
	bootstrapTestHome(t)
	repo := t.TempDir()
	plan, err := BuildPlan(Request{RepoRoot: repo, Profile: ProfileMinimal}, "test-version")
	if err != nil {
		t.Fatal(err)
	}
	report, err := Apply(plan, "test-version")
	if err != nil {
		t.Fatal(err)
	}
	agentsPath := filepath.Join(repo, "AGENTS.md")
	file, err := os.OpenFile(agentsPath, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteString("\nUser-owned addition.\n"); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}

	removal, err := Remove(plan)
	if err != nil {
		t.Fatal(err)
	}
	if removal.Status != RemovalComplete || len(removal.Candidates) != 0 {
		t.Fatalf("managed-block removal = %+v", removal)
	}
	for _, relative := range []string{".reconc/policy.lock.json", RepositoryReceiptRelativePath, report.ReceiptPath} {
		if _, err := os.Stat(filepath.Join(repo, filepath.FromSlash(relative))); !os.IsNotExist(err) {
			t.Fatalf("portable receipt-owned path still exists: %s: %v", relative, err)
		}
	}
	for _, relative := range []string{".reconc.yml", "AGENTS.md"} {
		if _, err := os.Stat(filepath.Join(repo, filepath.FromSlash(relative))); err != nil {
			t.Fatalf("user-owned path was removed: %s: %v", relative, err)
		}
	}
	remaining, err := os.ReadFile(agentsPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(remaining), "User-owned addition.") || strings.Contains(string(remaining), agentBlockStart) {
		t.Fatalf("managed removal changed outside bytes or retained its block: %s", remaining)
	}
}

func TestBootstrapRemoveStripsExactPreexistingManagedBlock(t *testing.T) {
	bootstrapTestHome(t)
	repo := t.TempDir()
	ignore, err := repositoryignore.Merge("vendor/\n")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, repositoryignore.RelativePath), []byte(ignore), 0o644); err != nil {
		t.Fatal(err)
	}
	plan, err := BuildPlan(Request{RepoRoot: repo, Profile: ProfileMinimal}, "test-version")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Apply(plan, "test-version"); err != nil {
		t.Fatal(err)
	}
	removal, err := Remove(plan)
	if err != nil {
		t.Fatal(err)
	}
	if removal.Status != RemovalComplete {
		t.Fatalf("removal = %+v", removal)
	}
	body, err := os.ReadFile(filepath.Join(repo, repositoryignore.RelativePath))
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "vendor/\n\n" {
		t.Fatalf("preexisting user ignore content = %q", body)
	}
}

func TestBootstrapRemoveRejectsTamperedReceiptBeforeMutation(t *testing.T) {
	bootstrapTestHome(t)
	repo := t.TempDir()
	plan, err := BuildPlan(Request{RepoRoot: repo, Profile: ProfileMinimal}, "test-version")
	if err != nil {
		t.Fatal(err)
	}
	_, err = Apply(plan, "test-version")
	if err != nil {
		t.Fatal(err)
	}
	receiptPath := filepath.Join(repo, filepath.FromSlash(RepositoryReceiptRelativePath))
	body, err := os.ReadFile(receiptPath)
	if err != nil {
		t.Fatal(err)
	}
	var receipt map[string]interface{}
	if err := json.Unmarshal(body, &receipt); err != nil {
		t.Fatal(err)
	}
	receipt["receipt_digest"] = strings.Repeat("0", 64)
	tampered, err := json.MarshalIndent(receipt, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(receiptPath, append(tampered, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Remove(plan); err == nil || !strings.Contains(err.Error(), "digest mismatch") {
		t.Fatalf("tampered receipt error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(repo, ".reconc.yml")); err != nil {
		t.Fatalf("tampered receipt caused product mutation: %v", err)
	}
}

func TestBootstrapRemoveSupportsLegacyPrivateReceipt(t *testing.T) {
	bootstrapTestHome(t)
	repo := t.TempDir()
	plan, err := BuildPlan(Request{RepoRoot: repo, Profile: ProfileMinimal}, "0.8.8")
	if err != nil {
		t.Fatal(err)
	}
	report, err := Apply(plan, "0.8.8")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(repo, filepath.FromSlash(RepositoryReceiptRelativePath))); err != nil {
		t.Fatal(err)
	}
	removal, err := Remove(plan)
	if err != nil || removal.Status != RemovalComplete {
		t.Fatalf("legacy removal = %+v err=%v", removal, err)
	}
	for _, relative := range []string{".reconc.yml", report.ReceiptPath, recordedPlanPath(plan)} {
		if _, err := os.Stat(filepath.Join(repo, filepath.FromSlash(relative))); !os.IsNotExist(err) {
			t.Fatalf("legacy receipt-owned path still exists: %s (%v)", relative, err)
		}
	}
}

func TestBootstrapLegacyRemoveStripsExactManagedBlock(t *testing.T) {
	bootstrapTestHome(t)
	repo := t.TempDir()
	agents, err := renderManagedDocument(
		repo,
		"AGENTS.md",
		"Repository instructions",
		agentBlockStart,
		agentBlockEnd,
		renderAgentBlock(),
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "AGENTS.md"), []byte(agents), 0o644); err != nil {
		t.Fatal(err)
	}
	plan, err := BuildPlan(Request{RepoRoot: repo, Profile: ProfileMinimal}, "0.8.8")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Apply(plan, "0.8.8"); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(repo, filepath.FromSlash(RepositoryReceiptRelativePath))); err != nil {
		t.Fatal(err)
	}
	removal, err := Remove(plan)
	if err != nil || removal.Status != RemovalComplete {
		t.Fatalf("legacy managed-block removal = %+v err=%v", removal, err)
	}
	body, err := os.ReadFile(filepath.Join(repo, "AGENTS.md"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), agentBlockStart) || string(body) != "# Repository instructions\n\n" {
		t.Fatalf("legacy managed block was not stripped exactly: %q", body)
	}
}

func TestBootstrapLegacyRemoveMaterializesManagedBlockCandidate(t *testing.T) {
	bootstrapTestHome(t)
	repo := t.TempDir()
	agents, err := renderManagedDocument(
		repo,
		"AGENTS.md",
		"Repository instructions",
		agentBlockStart,
		agentBlockEnd,
		renderAgentBlock(),
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "AGENTS.md"), []byte(agents), 0o644); err != nil {
		t.Fatal(err)
	}
	plan, err := BuildPlan(Request{RepoRoot: repo, Profile: ProfileMinimal}, "0.8.8")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Apply(plan, "0.8.8"); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(repo, filepath.FromSlash(RepositoryReceiptRelativePath))); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(repo, "AGENTS.md")
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	drifted := strings.Replace(string(body), "reconc refresh .", "reconc compile .", 1)
	if err := os.WriteFile(path, []byte(drifted), 0o644); err != nil {
		t.Fatal(err)
	}
	removal, err := Remove(plan)
	if err != nil || removal.Status != RemovalDrift || len(removal.Candidates) != 1 {
		t.Fatalf("legacy drift removal = %+v err=%v", removal, err)
	}
	if _, err := os.Stat(filepath.Join(repo, filepath.FromSlash(removal.Candidates[0]))); err != nil {
		t.Fatalf("legacy removal candidate missing: %v", err)
	}
}

func TestBootstrapRemoveMaterializesManagedBlockCandidateOnDrift(t *testing.T) {
	bootstrapTestHome(t)
	repo := t.TempDir()
	plan, err := BuildPlan(Request{RepoRoot: repo, Profile: ProfileMinimal}, "test-version")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Apply(plan, "test-version"); err != nil {
		t.Fatal(err)
	}
	agentsPath := filepath.Join(repo, "AGENTS.md")
	body, err := os.ReadFile(agentsPath)
	if err != nil {
		t.Fatal(err)
	}
	drifted := strings.Replace(string(body), "run `reconc refresh .`", "run `reconc compile .`", 1)
	if drifted == string(body) {
		t.Fatal("managed block fixture did not contain the expected command")
	}
	if err := os.WriteFile(agentsPath, []byte(drifted), 0o644); err != nil {
		t.Fatal(err)
	}
	removal, err := Remove(plan)
	if err != nil || removal.Status != RemovalDrift || len(removal.Candidates) != 1 {
		t.Fatalf("drift removal = %+v err=%v", removal, err)
	}
	if _, err := os.Stat(filepath.Join(repo, filepath.FromSlash(removal.Candidates[0]))); err != nil {
		t.Fatalf("removal candidate missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(repo, filepath.FromSlash(RepositoryReceiptRelativePath))); err != nil {
		t.Fatalf("drift removal consumed portable receipt: %v", err)
	}
}

func TestRemovalRollbackRefusesToOverwriteConcurrentChanges(t *testing.T) {
	for _, test := range []struct {
		name     string
		mutation removalMutation
	}{
		{name: "removed-path-reappeared", mutation: removalMutation{relative: "owned.txt", before: []byte("owned\n"), remove: true, mode: 0o644}},
		{name: "updated-path-changed", mutation: removalMutation{relative: "managed.txt", before: []byte("before\n"), after: []byte("after\n"), mode: 0o644}},
	} {
		t.Run(test.name, func(t *testing.T) {
			repo := t.TempDir()
			path := filepath.Join(repo, test.mutation.relative)
			test.mutation.path = path
			if err := os.WriteFile(path, []byte("external\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			if _, err := rollbackRemovalMutations(repo, []removalMutation{test.mutation}); err == nil || !strings.Contains(err.Error(), "refuse to overwrite") {
				t.Fatalf("rollback error = %v", err)
			}
			body, err := os.ReadFile(path)
			if err != nil || string(body) != "external\n" {
				t.Fatalf("concurrent content changed: body=%q err=%v", body, err)
			}
		})
	}
}

func TestApplyRemovalTransactionRejectsReplacementBeforeEachRemoval(t *testing.T) {
	for _, test := range []struct {
		name       string
		replace    func(*testing.T, string, string)
		wantPath   string
		wantBackup string
	}{
		{
			name: "regular replacement",
			replace: func(t *testing.T, path, replacement string) {
				if err := os.Rename(path, replacement); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte("attacker\n"), 0o644); err != nil {
					t.Fatal(err)
				}
			},
			wantPath: "attacker\n",
		},
		{
			name: "symlink substitution",
			replace: func(t *testing.T, path, replacement string) {
				if err := os.Rename(path, replacement); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(replacement, path); err != nil {
					t.Skipf("symlink unavailable: %v", err)
				}
			},
			wantPath:   "owned\n",
			wantBackup: "owned\n",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			repo := t.TempDir()
			path := filepath.Join(repo, "owned.txt")
			replacement := filepath.Join(repo, "replacement.txt")
			if err := os.WriteFile(path, []byte("owned\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			before, mode, identity, err := readRemovalSnapshot(path, maxBinaryBytes)
			if err != nil {
				t.Fatal(err)
			}
			mutation := removalMutation{relative: "owned.txt", path: path, before: before, mode: mode, remove: true, identity: identity}
			previousHook := beforeRemovalMutation
			beforeRemovalMutation = func(removalMutation) error {
				test.replace(t, path, replacement)
				return nil
			}
			t.Cleanup(func() { beforeRemovalMutation = previousHook })
			if _, _, _, err := applyRemovalTransaction(repo, []removalMutation{mutation}); err == nil {
				t.Fatal("replacement was removed")
			} else if !strings.Contains(err.Error(), "removal target") {
				t.Fatalf("replacement error = %v", err)
			}
			body, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if string(body) != test.wantPath {
				t.Fatalf("replacement body = %q", body)
			}
			if test.wantBackup != "" {
				if body, err := os.ReadFile(replacement); err != nil || string(body) != test.wantBackup {
					t.Fatalf("replacement target changed: body=%q err=%v", body, err)
				}
			}
		})
	}
}

func TestApplyRemovalTransactionRejectsReplacementAfterBinding(t *testing.T) {
	t.Run("leaf replacement", func(t *testing.T) {
		repo := t.TempDir()
		path := filepath.Join(repo, "owned.txt")
		verifiedPath := filepath.Join(repo, "verified.txt")
		if err := os.WriteFile(path, []byte("owned\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		before, mode, identity, err := readRemovalSnapshot(path, maxBinaryBytes)
		if err != nil {
			t.Fatal(err)
		}
		previousHook := beforeBoundRemoval
		beforeBoundRemoval = func(removalMutation) error {
			if err := os.Rename(path, verifiedPath); err != nil {
				return err
			}
			return os.WriteFile(path, []byte("attacker\n"), 0o644)
		}
		t.Cleanup(func() { beforeBoundRemoval = previousHook })
		mutation := removalMutation{relative: "owned.txt", path: path, before: before, mode: mode, remove: true, identity: identity}
		if _, _, _, err := applyRemovalTransaction(repo, []removalMutation{mutation}); err == nil {
			t.Fatal("leaf replacement was removed")
		}
		if body, err := os.ReadFile(path); err != nil || string(body) != "attacker\n" {
			t.Fatalf("replacement changed: body=%q err=%v", body, err)
		}
		if body, err := os.ReadFile(verifiedPath); err != nil || string(body) != "owned\n" {
			t.Fatalf("verified file changed: body=%q err=%v", body, err)
		}
	})

	t.Run("parent replacement", func(t *testing.T) {
		repo := t.TempDir()
		parentPath := filepath.Join(repo, "owned")
		if err := os.Mkdir(parentPath, 0o755); err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(parentPath, "target.txt")
		if err := os.WriteFile(path, []byte("owned\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		before, mode, identity, err := readRemovalSnapshot(path, maxBinaryBytes)
		if err != nil {
			t.Fatal(err)
		}
		verifiedParent := filepath.Join(repo, "verified-parent")
		attackerParent := t.TempDir()
		attackerPath := filepath.Join(attackerParent, "target.txt")
		if err := os.WriteFile(attackerPath, []byte("attacker\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		previousHook := beforeBoundRemoval
		beforeBoundRemoval = func(removalMutation) error {
			if err := os.Rename(parentPath, verifiedParent); err != nil {
				return err
			}
			return os.Symlink(attackerParent, parentPath)
		}
		t.Cleanup(func() { beforeBoundRemoval = previousHook })
		mutation := removalMutation{relative: "owned/target.txt", path: path, before: before, mode: mode, remove: true, identity: identity}
		if _, _, _, err := applyRemovalTransaction(repo, []removalMutation{mutation}); err == nil {
			t.Fatal("parent replacement was followed")
		}
		if body, err := os.ReadFile(attackerPath); err != nil || string(body) != "attacker\n" {
			t.Fatalf("attacker file changed: body=%q err=%v", body, err)
		}
		verifiedPath := filepath.Join(verifiedParent, "target.txt")
		if body, err := os.ReadFile(verifiedPath); err != nil || string(body) != "owned\n" {
			t.Fatalf("verified file changed: body=%q err=%v", body, err)
		}
	})
}

func TestApplyRemovalTransactionRollsBackAfterBoundParentSyncFailure(t *testing.T) {
	repo := t.TempDir()
	path := filepath.Join(repo, "owned.txt")
	before := []byte("owned\n")
	if err := os.WriteFile(path, before, 0o644); err != nil {
		t.Fatal(err)
	}
	current, mode, identity, err := readRemovalSnapshot(path, maxBinaryBytes)
	if err != nil {
		t.Fatal(err)
	}
	originalSync := bootstrapDirectorySync
	t.Cleanup(func() { bootstrapDirectorySync = originalSync })
	bootstrapDirectorySync = func(*os.Root) error {
		return errors.New("injected bound parent sync failure")
	}
	mutation := removalMutation{relative: "owned.txt", path: path, before: current, mode: mode, remove: true, identity: identity}
	_, _, rolledBack, err := applyRemovalTransaction(repo, []removalMutation{mutation})
	if err == nil || !strings.Contains(err.Error(), "bound parent sync failure") {
		t.Fatalf("sync failure = %v", err)
	}
	if strings.Join(rolledBack, ",") != "owned.txt" {
		t.Fatalf("rolled back = %v", rolledBack)
	}
	if body, err := os.ReadFile(path); err != nil || !bytes.Equal(body, before) {
		t.Fatalf("removed file was not restored: body=%q err=%v", body, err)
	}
}

func TestApplyRemovalTransactionRejectsReplacementBeforeBoundParentSync(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*testing.T, string)
	}{
		{
			name: "leaf replacement",
			mutate: func(t *testing.T, path string) {
				if err := os.WriteFile(path, []byte("attacker\n"), 0o644); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "parent replacement",
			mutate: func(t *testing.T, path string) {
				parent := filepath.Dir(path)
				if err := os.Rename(parent, parent+".verified"); err != nil {
					t.Fatal(err)
				}
				attackerParent := t.TempDir()
				if err := os.WriteFile(filepath.Join(attackerParent, filepath.Base(path)), []byte("attacker\n"), 0o644); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(attackerParent, parent); err != nil {
					t.Skipf("symlink unavailable: %v", err)
				}
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			repo := t.TempDir()
			parent := filepath.Join(repo, "owned")
			if err := os.Mkdir(parent, 0o755); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(parent, "target.txt")
			before := []byte("owned\n")
			if err := os.WriteFile(path, before, 0o644); err != nil {
				t.Fatal(err)
			}
			current, mode, identity, err := readRemovalSnapshot(path, maxBinaryBytes)
			if err != nil {
				t.Fatal(err)
			}
			previousHook := beforeBoundRemovalSync
			beforeBoundRemovalSync = func(*os.Root, string) error {
				test.mutate(t, path)
				return nil
			}
			t.Cleanup(func() { beforeBoundRemovalSync = previousHook })
			mutation := removalMutation{relative: "owned/target.txt", path: path, before: current, mode: mode, remove: true, identity: identity}
			if _, _, _, err := applyRemovalTransaction(repo, []removalMutation{mutation}); err == nil {
				t.Fatal("replacement before parent sync was accepted")
			}
			if body, err := os.ReadFile(path); err != nil || string(body) != "attacker\n" {
				t.Fatalf("replacement changed: body=%q err=%v", body, err)
			}
		})
	}
}

func TestApplyRemovalTransactionRollsBackEarlierRemovalAfterReplacement(t *testing.T) {
	repo := t.TempDir()
	firstPath := filepath.Join(repo, "first.txt")
	secondPath := filepath.Join(repo, "second.txt")
	if err := os.WriteFile(firstPath, []byte("first\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(secondPath, []byte("second\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	firstBefore, firstMode, firstIdentity, err := readRemovalSnapshot(firstPath, maxBinaryBytes)
	if err != nil {
		t.Fatal(err)
	}
	secondBefore, secondMode, secondIdentity, err := readRemovalSnapshot(secondPath, maxBinaryBytes)
	if err != nil {
		t.Fatal(err)
	}
	mutations := []removalMutation{
		{relative: "first.txt", path: firstPath, before: firstBefore, mode: firstMode, remove: true, identity: firstIdentity},
		{relative: "second.txt", path: secondPath, before: secondBefore, mode: secondMode, remove: true, identity: secondIdentity},
	}
	calls := 0
	previousHook := beforeRemovalMutation
	beforeRemovalMutation = func(removalMutation) error {
		calls++
		if calls == 2 {
			if err := os.Rename(secondPath, secondPath+".owned"); err != nil {
				return err
			}
			if err := os.WriteFile(secondPath, []byte("attacker\n"), 0o644); err != nil {
				return err
			}
		}
		return nil
	}
	t.Cleanup(func() { beforeRemovalMutation = previousHook })
	_, _, rolledBack, err := applyRemovalTransaction(repo, mutations)
	if err == nil || !strings.Contains(err.Error(), "identity") {
		t.Fatalf("replacement transaction error = %v", err)
	}
	if strings.Join(rolledBack, ",") != "first.txt" {
		t.Fatalf("rolled back = %v", rolledBack)
	}
	if body, err := os.ReadFile(firstPath); err != nil || string(body) != "first\n" {
		t.Fatalf("first removal was not restored: body=%q err=%v", body, err)
	}
	if body, err := os.ReadFile(secondPath); err != nil || string(body) != "attacker\n" {
		t.Fatalf("second replacement was changed: body=%q err=%v", body, err)
	}
}

func TestRemovalRollbackRestoresRemovedAndUpdatedFiles(t *testing.T) {
	repo := t.TempDir()
	removedPath := filepath.Join(repo, "nested", "removed.txt")
	if err := os.MkdirAll(filepath.Dir(removedPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(removedPath, []byte("before\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	updatedPath := filepath.Join(repo, "updated.txt")
	if err := os.WriteFile(updatedPath, []byte("original\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	applied := []removalMutation{
		{relative: "nested/removed.txt", path: removedPath, before: []byte("before\n"), mode: 0o644, remove: true},
		{relative: "updated.txt", path: updatedPath, before: []byte("original\n"), after: []byte("changed\n"), mode: 0o644},
	}
	// Simulate the forward removal: delete one file, rewrite the other.
	if err := os.Remove(removedPath); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Dir(removedPath)); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(updatedPath, []byte("changed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	rolledBack, err := rollbackRemovalMutations(repo, applied)
	if err != nil {
		t.Fatalf("rollback error = %v", err)
	}
	if strings.Join(rolledBack, ",") != "nested/removed.txt,updated.txt" {
		t.Fatalf("rolled back = %v", rolledBack)
	}
	if body, err := os.ReadFile(removedPath); err != nil || string(body) != "before\n" {
		t.Fatalf("removed file not restored: body=%q err=%v", body, err)
	}
	if body, err := os.ReadFile(updatedPath); err != nil || string(body) != "original\n" {
		t.Fatalf("updated file not restored: body=%q err=%v", body, err)
	}
}
