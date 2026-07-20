package hooks

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestInstallRejectsSymlinkedManagedParentDirectories(t *testing.T) {
	tests := []struct {
		kind   string
		target string
	}{
		{kind: KindClaudeCode, target: ClaudeCodeSettingsPath},
		{kind: KindCodex, target: CodexHooksPath},
		{kind: KindCursor, target: CursorHooksPath},
		{kind: KindOpenCode, target: OpenCodePluginPath},
		{kind: KindDevinCLI, target: DevinHooksPath},
		{kind: KindGrok, target: GrokHooksPath},
		{kind: KindAntigravity, target: AntigravityHooksPath},
		{kind: KindKilo, target: KiloPluginPath},
	}
	for _, test := range tests {
		t.Run(test.kind, func(t *testing.T) {
			repo := t.TempDir()
			outside := t.TempDir()
			firstComponent := strings.Split(filepath.ToSlash(test.target), "/")[0]
			createDirectoryLinkForTest(t, outside, filepath.Join(repo, firstComponent))
			if _, err := Install(test.kind, repo, true); err == nil || !strings.Contains(err.Error(), "resolves outside") {
				t.Fatalf("expected parent-symlink rejection, got %v", err)
			}
			outsideTarget := filepath.Join(outside, filepath.FromSlash(strings.TrimPrefix(test.target, firstComponent+"/")))
			if _, err := os.Lstat(outsideTarget); !os.IsNotExist(err) {
				t.Fatalf("external target was created or became unreadable: %v", err)
			}
		})
	}
}

func TestScaffoldSyncPreflightsEveryManagedParentBeforeWriting(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	createDirectoryLinkForTest(t, outside, filepath.Join(root, ".claude"))
	if _, err := SyncRepoRootScaffold(root); err == nil || !strings.Contains(err.Error(), "resolves outside") {
		t.Fatalf("expected scaffold parent-symlink rejection, got %v", err)
	}
	entries, err := os.ReadDir(outside)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("scaffold sync wrote outside its root: %v", entries)
	}
	if _, err := os.Stat(filepath.Join(root, GitPreCommitScaffoldPath)); !os.IsNotExist(err) {
		t.Fatalf("scaffold sync wrote before completing preflight: %v", err)
	}
}

func TestInstallRejectsOversizedManagedTargets(t *testing.T) {
	for _, test := range []struct {
		kind   string
		target string
	}{
		{kind: KindClaudeCode, target: ClaudeCodeSettingsPath},
		{kind: KindOpenCode, target: OpenCodePluginPath},
		{kind: KindGrok, target: GrokHooksPath},
	} {
		t.Run(test.kind, func(t *testing.T) {
			repo := t.TempDir()
			target := filepath.Join(repo, filepath.FromSlash(test.target))
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				t.Fatal(err)
			}
			file, err := os.Create(target)
			if err != nil {
				t.Fatal(err)
			}
			if err := file.Truncate(maxManagedArtifactBytes + 1); err != nil {
				_ = file.Close()
				t.Fatal(err)
			}
			if err := file.Close(); err != nil {
				t.Fatal(err)
			}
			if _, err := Install(test.kind, repo, true); err == nil || !strings.Contains(err.Error(), "managed-artifact limit") {
				t.Fatalf("expected bounded-read rejection, got %v", err)
			}
		})
	}
}

func TestMalformedConfigBackupCollisionFailsClosed(t *testing.T) {
	target := filepath.Join(t.TempDir(), "settings.json")
	source := []byte("malformed")
	backup, err := backupMalformedConfig(target, source)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(backup)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0o600 {
			t.Fatalf("malformed-config backup mode = %o, want 600", info.Mode().Perm())
		}
	}
	if err := os.WriteFile(backup, []byte("different"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := backupMalformedConfig(target, source); err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("expected colliding backup rejection, got %v", err)
	}
}
