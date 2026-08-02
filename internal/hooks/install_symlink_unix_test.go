//go:build !windows

package hooks

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInstallRejectsSymlinkedManagedTargets(t *testing.T) {
	tests := []struct {
		kind   string
		target string
	}{
		{kind: KindClaudeCode, target: ClaudeCodeSettingsPath},
		{kind: KindCodex, target: CodexHooksPath},
		{kind: KindGitHubCopilot, target: GitHubCopilotHooksPath},
		{kind: KindCursor, target: CursorHooksPath},
		{kind: KindOpenCode, target: OpenCodePluginPath},
		{kind: KindDevinCLI, target: DevinHooksPath},
		{kind: KindGrok, target: GrokHooksPath},
		{kind: KindAntigravity, target: AntigravityHooksPath},
		{kind: KindKilo, target: KiloPluginPath},
		{kind: KindOMP, target: OMPExtensionPath},
	}
	for _, test := range tests {
		t.Run(test.kind, func(t *testing.T) {
			repo := t.TempDir()
			target := filepath.Join(repo, filepath.FromSlash(test.target))
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				t.Fatal(err)
			}
			outside := filepath.Join(t.TempDir(), "outside")
			original := []byte("preserve me\n")
			if err := os.WriteFile(outside, original, 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(outside, target); err != nil {
				t.Fatal(err)
			}
			if _, err := Install(test.kind, repo, true); err == nil || !strings.Contains(err.Error(), "resolves outside") {
				t.Fatalf("expected symlink rejection, got %v", err)
			}
			got, err := os.ReadFile(outside)
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != string(original) {
				t.Fatalf("outside target changed: %q", got)
			}
			backups, err := filepath.Glob(target + ".reconc-backup-*")
			if err != nil {
				t.Fatal(err)
			}
			if len(backups) != 0 {
				t.Fatalf("symlinked external bytes leaked into backup files: %v", backups)
			}
		})
	}
}

func TestMalformedConfigBackupReuseRepairsPrivateMode(t *testing.T) {
	target := filepath.Join(t.TempDir(), "settings.json")
	source := []byte("malformed")
	sum := sha256.Sum256(source)
	backup := target + ".reconc-backup-" + hex.EncodeToString(sum[:4])
	if err := os.WriteFile(backup, source, 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := backupMalformedConfig(target, source)
	if err != nil || got != backup {
		t.Fatalf("reuse backup = %q, %v", got, err)
	}
	info, err := os.Stat(backup)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("reused backup mode = %o, want 600", info.Mode().Perm())
	}
}
