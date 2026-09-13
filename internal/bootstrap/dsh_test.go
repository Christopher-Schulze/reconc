package bootstrap

import (
	"bytes"
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
	if err != nil || status.State != hooks.StateConfigured || !status.Configured || status.Live {
		t.Fatalf("DSH bootstrap status = %+v, %v", status, err)
	}
	file, err := os.OpenFile(filepath.Join(repo, filepath.FromSlash(hooks.DSHPatchPath)), os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	addition := "\n- id: agent-loop\n  maxSteps: 12\n"
	_, writeErr := file.WriteString(addition)
	closeErr := file.Close()
	if writeErr != nil || closeErr != nil {
		t.Fatalf("edit DSH patch: write=%v close=%v", writeErr, closeErr)
	}
	drift, err := BuildSyncPlan(repo, "test-version")
	if err != nil {
		t.Fatal(err)
	}
	action := syncActionForPath(t, drift, hooks.DSHPatchPath)
	if action.State != SyncUserDrift || len(drift.BlockingIssues) == 0 {
		t.Fatalf("edited DSH patch was not protected: %+v", drift)
	}
	refused, err := ApplySyncPlan(drift, drift.PlanDigest, "test-version")
	if err == nil || refused.Status != SyncRefused || len(refused.Changed) != 0 {
		t.Fatalf("edited DSH patch sync = %+v, %v", refused, err)
	}
	got, err := os.ReadFile(filepath.Join(repo, filepath.FromSlash(hooks.DSHPatchPath)))
	if err != nil || !bytes.Equal(got, append(patch, []byte(addition)...)) {
		t.Fatalf("refused sync changed DSH patch: %q, %v", got, err)
	}
}
