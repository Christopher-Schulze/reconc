package bootstrap

import (
	"encoding/json"
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
			path := filepath.Join(t.TempDir(), test.mutation.relative)
			test.mutation.path = path
			if err := os.WriteFile(path, []byte("external\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			if _, err := rollbackRemovalMutations([]removalMutation{test.mutation}); err == nil || !strings.Contains(err.Error(), "refuse to overwrite") {
				t.Fatalf("rollback error = %v", err)
			}
			body, err := os.ReadFile(path)
			if err != nil || string(body) != "external\n" {
				t.Fatalf("concurrent content changed: body=%q err=%v", body, err)
			}
		})
	}
}
