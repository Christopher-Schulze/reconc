//go:build !windows

package ingest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFragmentOutsideRootProducesFilesystemIdentityWarning(t *testing.T) {
	outside := t.TempDir()
	repo := t.TempDir()
	writeFile(t, outside, "extra.yml", "rules: []\n")
	writeFile(t, repo, ".reconc.yml", "default_mode: warn\ninclude:\n  - policies/*.yml\n")
	if err := os.MkdirAll(filepath.Join(repo, "policies"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(outside, "extra.yml"), filepath.Join(repo, "policies", "extra.yml")); err != nil {
		t.Fatal(err)
	}
	bundle, err := LoadPolicySources(repo)
	if err != nil {
		t.Fatal(err)
	}
	for _, warning := range bundle.Discovery.Warnings {
		if strings.Contains(warning, "policies/extra.yml") &&
			strings.Contains(warning, "outside the repository root via filesystem indirection") {
			return
		}
	}
	t.Fatalf("missing filesystem-identity warning: %v", bundle.Discovery.Warnings)
}
