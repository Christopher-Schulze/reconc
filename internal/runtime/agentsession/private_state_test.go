package agentsession

import (
	"os"
	"path/filepath"
	"testing"

	"reconc.dev/reconc/internal/privatefs"
)

func TestEnsurePrivateStateDirRepairsOwnedDirectory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "owned-state")
	if err := os.Mkdir(path, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := ensurePrivateStateDir(path); err != nil {
		t.Fatalf("repair owned state directory: %v", err)
	}
	if err := privatefs.ValidateDirectory(path); err != nil {
		t.Fatalf("validate repaired owned state directory: %v", err)
	}
}
