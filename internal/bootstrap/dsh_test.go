package bootstrap

import (
	"os"
	"path/filepath"
	"testing"

	"reconc.dev/reconc/internal/hooks"
)

func TestGovernedBootstrapInstallsCompleteDSHOverlay(t *testing.T) {
	bootstrapTestHome(t)
	repo := t.TempDir()
	plan, err := BuildPlan(Request{
		RepoRoot: repo, Profile: ProfileGoverned, Hooks: []string{hooks.KindDSH},
	}, "test-version")
	if err != nil {
		t.Fatal(err)
	}
	selected := map[string]bool{}
	for _, action := range plan.Actions {
		selected[action.Path] = true
	}
	for _, path := range []string{hooks.DSHExtensionPath, hooks.DSHPatchPath, hooks.WrapperPath} {
		if !selected[path] {
			t.Fatalf("DSH bootstrap plan omitted %s", path)
		}
	}
	report, err := Apply(plan, "test-version")
	if err != nil || report.Status != ApplyComplete {
		t.Fatalf("DSH bootstrap apply = %+v, %v", report, err)
	}
	verification, err := Verify(plan)
	if err != nil || !verification.Valid {
		t.Fatalf("DSH bootstrap verify = %+v, %v", verification, err)
	}
	syncVerification, err := VerifyRepository(repo, "test-version")
	if err != nil || !syncVerification.Valid {
		t.Fatalf("DSH repository sync verify = %+v, %v", syncVerification, err)
	}
	syncPlan, err := BuildSyncPlan(repo, "test-version")
	if err != nil {
		t.Fatal(err)
	}
	syncPaths := map[string]bool{}
	for _, action := range syncPlan.Actions {
		if action.State != SyncUnchanged {
			t.Fatalf("fresh DSH sync action = %+v", action)
		}
		syncPaths[action.Path] = true
	}
	for _, path := range []string{hooks.DSHExtensionPath, hooks.DSHPatchPath} {
		if !syncPaths[path] {
			t.Fatalf("repository sync did not own %s", path)
		}
	}
	patch, err := os.ReadFile(filepath.Join(repo, filepath.FromSlash(hooks.DSHPatchPath)))
	if err != nil || string(patch) != hooks.GenerateDSHPatch().Content {
		t.Fatalf("installed DSH profile overlay differs from generator: %q, %v", patch, err)
	}
	status, err := hooks.InspectPlatform(repo, hooks.KindDSH)
	if err != nil || status.State != hooks.StateInstalled || status.Configured || status.Live {
		t.Fatalf("DSH bootstrap status = %+v, %v", status, err)
	}
}
