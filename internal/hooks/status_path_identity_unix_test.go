//go:build !windows

package hooks

import (
	"os"
	"path/filepath"
	"testing"
)

func TestInspectPlatformsResolvesRepositorySymlink(t *testing.T) {
	repo := t.TempDir()
	alias := filepath.Join(t.TempDir(), "repo-alias")
	if err := os.Symlink(repo, alias); err != nil {
		t.Fatal(err)
	}
	reports, err := InspectPlatforms(alias)
	if err != nil {
		t.Fatal(err)
	}
	if len(reports) != len(SupportedKinds()) {
		t.Fatalf("reports=%d kinds=%d", len(reports), len(SupportedKinds()))
	}
	for _, report := range reports {
		if report.State != StateAbsent {
			t.Fatalf("%s=%s, want absent through repository alias", report.Kind, report.State)
		}
	}
}
