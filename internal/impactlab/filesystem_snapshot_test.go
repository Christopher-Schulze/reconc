package impactlab

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRepositorySnapshotRejectsSameMetadataContentDrift(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "evidence.txt")
	if err := os.WriteFile(path, []byte("first"), 0o600); err != nil {
		t.Fatal(err)
	}
	snapshot, err := captureRepositorySnapshot(root)
	if err != nil {
		t.Fatal(err)
	}
	original, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("other"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, original.ModTime(), original.ModTime()); err != nil {
		t.Fatal(err)
	}

	err = snapshot.revalidate(root)
	if err == nil || !strings.Contains(err.Error(), "evidence.txt") {
		t.Fatalf("revalidate() error = %v, want evidence path drift", err)
	}
}
