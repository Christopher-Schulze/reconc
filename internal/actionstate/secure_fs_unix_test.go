//go:build !windows

package actionstate

import (
	"os"
	"path/filepath"
	"testing"
)

func TestEnsurePrivateDirectoryDoesNotRepermissionExistingRoot(t *testing.T) {
	root := filepath.Join(t.TempDir(), "shared-root")
	if err := os.Mkdir(root, 0o755); err != nil {
		t.Fatal(err)
	}
	before, err := os.Stat(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := ensurePrivateDirectory(root); err == nil {
		t.Fatal("non-private existing root was accepted")
	}
	after, err := os.Stat(root)
	if err != nil {
		t.Fatal(err)
	}
	if after.Mode().Perm() != before.Mode().Perm() {
		t.Fatalf("existing root mode changed from %04o to %04o", before.Mode().Perm(), after.Mode().Perm())
	}
}
