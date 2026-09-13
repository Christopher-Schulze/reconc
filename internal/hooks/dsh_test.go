package hooks

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDSHInstallStatusAndExactRemoval(t *testing.T) {
	repo := t.TempDir()
	gitInitRepo(t, repo)
	sibling := filepath.Join(repo, ".dsh", "user.patch.yml")
	if err := os.MkdirAll(filepath.Dir(sibling), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(sibling, []byte("user-owned: true\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	first, err := Install(KindDSH, repo, false)
	if err != nil || first == nil || first.ActivationPath != DSHPatchPath {
		t.Fatalf("first DSH install = %+v, %v", first, err)
	}
	for _, relative := range []string{DSHExtensionPath, DSHPatchPath, WrapperPath} {
		if _, err := os.Stat(filepath.Join(repo, filepath.FromSlash(relative))); err != nil {
			t.Fatalf("installed %s: %v", relative, err)
		}
	}
	if _, err := Install(KindDSH, repo, false); err != nil {
		t.Fatalf("idempotent DSH install: %v", err)
	}
	status, err := InspectPlatform(repo, KindDSH)
	if err != nil || status.State != StateInstalled || !status.Installed || status.Configured || status.Live {
		t.Fatalf("DSH static status = %+v, %v", status, err)
	}
	removed, err := Uninstall(KindDSH, repo)
	if err != nil || removed == nil || removed.RemovedEntries != 2 || removed.ActivationAction != "removed-managed-patch" {
		t.Fatalf("DSH removal = %+v, %v", removed, err)
	}
	for _, relative := range []string{DSHExtensionPath, DSHPatchPath} {
		if _, err := os.Stat(filepath.Join(repo, filepath.FromSlash(relative))); !os.IsNotExist(err) {
			t.Fatalf("removed DSH file %s remains: %v", relative, err)
		}
	}
	if body, err := os.ReadFile(sibling); err != nil || string(body) != "user-owned: true\n" {
		t.Fatalf("sibling DSH file changed: %q, %v", body, err)
	}
	if _, err := os.Stat(filepath.Join(repo, filepath.FromSlash(WrapperPath))); err != nil {
		t.Fatalf("shared wrapper was removed: %v", err)
	}
}

func TestDSHForeignAndDriftedPatchRefuseMutation(t *testing.T) {
	for _, test := range []struct {
		name    string
		drifted bool
	}{
		{name: "foreign before install"},
		{name: "drifted before uninstall", drifted: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			repo := t.TempDir()
			gitInitRepo(t, repo)
			patchPath := filepath.Join(repo, filepath.FromSlash(DSHPatchPath))
			if err := os.MkdirAll(filepath.Dir(patchPath), 0o755); err != nil {
				t.Fatal(err)
			}
			if test.drifted {
				if _, err := Install(KindDSH, repo, false); err != nil {
					t.Fatal(err)
				}
				file, err := os.OpenFile(patchPath, os.O_APPEND|os.O_WRONLY, 0)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := file.WriteString("# user change\n"); err != nil {
					t.Fatal(err)
				}
				if err := file.Close(); err != nil {
					t.Fatal(err)
				}
				if _, err := Uninstall(KindDSH, repo); err == nil || !strings.Contains(err.Error(), "refusing to delete") {
					t.Fatalf("drifted patch removal error = %v", err)
				}
				if _, err := os.Stat(filepath.Join(repo, filepath.FromSlash(DSHExtensionPath))); err != nil {
					t.Fatalf("failed removal deleted extension: %v", err)
				}
			} else {
				if err := os.WriteFile(patchPath, []byte("user-owned: true\n"), 0o644); err != nil {
					t.Fatal(err)
				}
				if _, err := Install(KindDSH, repo, true); err == nil || !strings.Contains(err.Error(), "not Reconc-managed") {
					t.Fatalf("foreign patch install error = %v", err)
				}
				if _, err := os.Stat(filepath.Join(repo, filepath.FromSlash(DSHExtensionPath))); !os.IsNotExist(err) {
					t.Fatalf("failed installation created extension: %v", err)
				}
			}
			if body, err := os.ReadFile(patchPath); err != nil || !strings.Contains(string(body), "user") {
				t.Fatalf("user patch changed: %q, %v", body, err)
			}
		})
	}
}
