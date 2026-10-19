package hooks

import (
	"os"
	"path/filepath"
	"testing"
)

func TestInstallTreatsANullHostConfigAsEmpty(t *testing.T) {
	for _, kind := range []string{KindClaudeCode, KindCodex, KindCursor, KindDevinCLI, KindAntigravity, KindZCode} {
		t.Run(kind, func(t *testing.T) {
			repo := t.TempDir()
			platform, ok := PlatformForKind(kind)
			if !ok {
				t.Fatalf("unknown kind %s", kind)
			}
			target := filepath.Join(repo, filepath.FromSlash(platform.TargetPath))
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(target, []byte("null\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			if _, err := Install(kind, repo, false); err != nil {
				t.Fatalf("install over a null config: %v", err)
			}
			if status := statusForKind(t, repo, kind); status.State == StateAbsent || status.State == StateDegraded && status.Detail == "artifact is invalid JSON; reinstall the hook" {
				t.Fatalf("null config install did not write the generated entries: %+v", status)
			}
		})
	}
}
