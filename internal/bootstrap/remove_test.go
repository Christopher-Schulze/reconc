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
	for _, relative := range []string{".gitignore", ".reconc.yml", ".reconc/policy.lock.json", "AGENTS.md", report.ReceiptPath} {
		if _, err := os.Stat(filepath.Join(repo, filepath.FromSlash(relative))); !os.IsNotExist(err) {
			t.Fatalf("receipt-owned path still exists: %s (%v)", relative, err)
		}
	}
	if body, err := os.ReadFile(userPath); err != nil || string(body) != "keep\n" {
		t.Fatalf("user file changed: body=%q err=%v", body, err)
	}
}

func TestBootstrapRemovePreservesAllPrimaryArtifactsWhenManagedFileDrifts(t *testing.T) {
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
	if removal.Status != RemovalDrift || len(removal.Candidates) != 1 {
		t.Fatalf("drift removal = %+v", removal)
	}
	for _, relative := range []string{".reconc.yml", ".reconc/policy.lock.json", report.ReceiptPath} {
		if _, err := os.Stat(filepath.Join(repo, filepath.FromSlash(relative))); err != nil {
			t.Fatalf("primary removal occurred before drift resolution: %s: %v", relative, err)
		}
	}
	candidate, err := os.ReadFile(filepath.Join(repo, filepath.FromSlash(removal.Candidates[0])))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(candidate), "User-owned addition.") || strings.Contains(string(candidate), agentBlockStart) {
		t.Fatalf("removal candidate is not marker-only: %s", candidate)
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
	report, err := Apply(plan, "test-version")
	if err != nil {
		t.Fatal(err)
	}
	receiptPath := filepath.Join(repo, filepath.FromSlash(report.ReceiptPath))
	body, err := os.ReadFile(receiptPath)
	if err != nil {
		t.Fatal(err)
	}
	var receipt map[string]interface{}
	if err := json.Unmarshal(body, &receipt); err != nil {
		t.Fatal(err)
	}
	receipt["digest"] = strings.Repeat("0", 64)
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
