package bootstrap

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"reconc.dev/reconc/internal/hooks"
)

func TestBootstrapOwnsDirectHookTargetWithHostBinary(t *testing.T) {
	bootstrapTestHome(t)
	repo := t.TempDir()
	source := filepath.Join(t.TempDir(), "reconc-release")
	if err := os.WriteFile(source, []byte("binary-content"), 0o755); err != nil {
		t.Fatal(err)
	}
	selection, err := BinarySelectionFor(source, "", runtime.GOOS, runtime.GOARCH)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := BuildPlan(Request{RepoRoot: repo, Profile: ProfileGoverned, Binary: selection}, "test-version")
	if err != nil {
		t.Fatal(err)
	}
	report, err := Apply(plan, "test-version")
	if err != nil || report.Status != ApplyComplete {
		t.Fatalf("apply: report=%+v err=%v", report, err)
	}
	target, err := hooks.GenerateWrapperTarget(runtime.GOOS, runtime.GOARCH)
	if err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(filepath.Join(repo, filepath.FromSlash(target.TargetPath)))
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != target.Content {
		t.Fatalf("direct target=%q, want %q", body, target.Content)
	}
	receipt, err := LoadRepositoryReceipt(repo)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, file := range receipt.ManagedFiles {
		if file.Path == hooks.WrapperTargetPath {
			found = file.Component == "hook-wrapper-target" && file.Mode == 0o644
		}
	}
	if !found {
		t.Fatalf("portable receipt does not own %s: %+v", hooks.WrapperTargetPath, receipt.ManagedFiles)
	}
}

func TestBootstrapDirectHookTargetRollsBackWithTransaction(t *testing.T) {
	bootstrapTestHome(t)
	repo := t.TempDir()
	source := filepath.Join(t.TempDir(), "reconc-release")
	if err := os.WriteFile(source, []byte("binary-content"), 0o755); err != nil {
		t.Fatal(err)
	}
	selection, err := BinarySelectionFor(source, "", runtime.GOOS, runtime.GOARCH)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := BuildPlan(Request{RepoRoot: repo, Profile: ProfileGoverned, Binary: selection}, "test-version")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := apply(plan, "test-version", applyOptions{failAfter: len(plan.Actions) - 1}); err == nil {
		t.Fatal("injected transaction failure succeeded")
	}
	if remaining := bootstrapTreeSnapshot(t, repo); len(remaining) != 0 {
		t.Fatalf("rollback left direct-target transaction state: %v", remaining)
	}
}

func TestCrossPlatformBinaryDoesNotPublishHostDirectTarget(t *testing.T) {
	bootstrapTestHome(t)
	repo := t.TempDir()
	targetOS, targetArch := "linux", "amd64"
	if runtime.GOOS == targetOS && runtime.GOARCH == targetArch {
		targetOS = "darwin"
	}
	source := filepath.Join(t.TempDir(), "reconc-release")
	if err := os.WriteFile(source, []byte("binary-content"), 0o755); err != nil {
		t.Fatal(err)
	}
	selection, err := BinarySelectionFor(source, "", targetOS, targetArch)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := BuildPlan(Request{RepoRoot: repo, Profile: ProfileGoverned, Binary: selection}, "test-version")
	if err != nil {
		t.Fatal(err)
	}
	for _, action := range plan.Actions {
		if action.Path == hooks.WrapperTargetPath {
			t.Fatalf("cross-platform binary published current-host direct target: %+v", action)
		}
	}
}
