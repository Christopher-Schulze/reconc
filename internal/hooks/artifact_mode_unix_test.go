//go:build !windows

package hooks

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"reconc.dev/reconc/internal/gitexec"
)

func TestGeneratedArtifactPublicationPreservesStrictUnixModes(t *testing.T) {
	for _, test := range []struct {
		name       string
		before     string
		after      string
		mode       os.FileMode
		executable bool
		wantMode   os.FileMode
		wantAction string
	}{
		{name: "strict data unchanged", before: "same", after: "same", mode: 0o600, wantMode: 0o600, wantAction: "unchanged"},
		{name: "strict data updated", before: "before", after: "after", mode: 0o600, wantMode: 0o600, wantAction: "updated"},
		{name: "strict executable unchanged", before: "same", after: "same", mode: 0o700, executable: true, wantMode: 0o700, wantAction: "unchanged"},
		{name: "strict executable updated", before: "before", after: "after", mode: 0o700, executable: true, wantMode: 0o700, wantAction: "updated"},
		{name: "missing owner execute repaired", before: "same", after: "same", mode: 0o600, executable: true, wantMode: 0o700, wantAction: "updated"},
		{name: "preferred executable unchanged", before: "same", after: "same", mode: 0o755, executable: true, wantMode: 0o755, wantAction: "unchanged"},
	} {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "artifact")
			if err := os.WriteFile(path, []byte(test.before), test.mode); err != nil {
				t.Fatal(err)
			}
			if err := os.Chmod(path, test.mode); err != nil {
				t.Fatal(err)
			}
			snapshot, err := readManagedArtifactSnapshot(path)
			if err != nil {
				t.Fatal(err)
			}
			action, err := writeGeneratedArtifact(path, test.after, test.executable, snapshot)
			if err != nil {
				t.Fatal(err)
			}
			info, err := os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}
			if action != test.wantAction || info.Mode().Perm() != test.wantMode {
				t.Fatalf("publication = action %q mode %04o, want action %q mode %04o", action, info.Mode().Perm(), test.wantAction, test.wantMode)
			}
		})
	}
}

func TestInstallGitPreCommitPreservesStrictModeAndRepairsOwnerExecute(t *testing.T) {
	repo := t.TempDir()
	command := gitexec.CommandContext(context.Background(), repo, nil, "init", "--quiet")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("initialize repository: %v: %s", err, output)
	}
	if _, err := Install(KindGitPreCommit, repo, false); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(repo, ".git", "hooks", "pre-commit")
	if err := os.Chmod(target, 0o700); err != nil {
		t.Fatal(err)
	}
	report, err := Install(KindGitPreCommit, repo, false)
	if err != nil {
		t.Fatal(err)
	}
	if report.Action != "unchanged" {
		t.Fatalf("strict reinstall action = %q, want unchanged", report.Action)
	}
	assertHookArtifactMode(t, target, 0o700)
	status, err := InspectPlatform(repo, KindGitPreCommit)
	if err != nil {
		t.Fatal(err)
	}
	if !status.Executable || status.State != StateConfigured {
		t.Fatalf("strict executable status = %+v", status)
	}
	if err := os.Chmod(target, 0o600); err != nil {
		t.Fatal(err)
	}
	report, err = Install(KindGitPreCommit, repo, false)
	if err != nil {
		t.Fatal(err)
	}
	if report.Action != "updated" {
		t.Fatalf("execute repair action = %q, want updated", report.Action)
	}
	assertHookArtifactMode(t, target, 0o700)
}

func assertHookArtifactMode(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != want {
		t.Fatalf("artifact mode = %04o, want %04o", info.Mode().Perm(), want)
	}
}
