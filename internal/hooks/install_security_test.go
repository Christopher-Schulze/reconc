package hooks

import (
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
		{kind: KindOpenCode, target: OpenCodePluginPath},
		{kind: KindGrok, target: GrokHooksPath},
		{kind: KindAntigravity, target: AntigravityHooksPath},
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
			if _, err := Install(test.kind, repo, true); err == nil || !strings.Contains(err.Error(), "not a regular file") {
				t.Fatalf("expected symlink rejection, got %v", err)
			}
			got, err := os.ReadFile(outside)
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != string(original) {
				t.Fatalf("outside target changed: %q", got)
			}
		})
	}
}
