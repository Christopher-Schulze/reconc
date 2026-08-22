package hooks

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestManagedArtifactSnapshotRejectsConcurrentEditsAndReplacement(t *testing.T) {
	t.Run("unchanged", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "config.json")
		if err := os.WriteFile(path, []byte("before"), 0o640); err != nil {
			t.Fatal(err)
		}
		snapshot, err := readManagedArtifactSnapshot(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := revalidateManagedArtifactSnapshot(path, snapshot); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("same identity changed bytes", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "config.json")
		if err := os.WriteFile(path, []byte("before"), 0o640); err != nil {
			t.Fatal(err)
		}
		snapshot, err := readManagedArtifactSnapshot(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("after!"), 0o640); err != nil {
			t.Fatal(err)
		}
		if err := revalidateManagedArtifactSnapshot(path, snapshot); err == nil || !strings.Contains(err.Error(), "changed after install preflight") {
			t.Fatalf("changed bytes revalidation error = %v", err)
		}
	})

	t.Run("same bytes replaced identity", func(t *testing.T) {
		directory := t.TempDir()
		path := filepath.Join(directory, "config.json")
		if err := os.WriteFile(path, []byte("stable"), 0o640); err != nil {
			t.Fatal(err)
		}
		snapshot, err := readManagedArtifactSnapshot(path)
		if err != nil {
			t.Fatal(err)
		}
		replacement := filepath.Join(directory, "replacement.json")
		if err := os.WriteFile(replacement, []byte("stable"), 0o640); err != nil {
			t.Fatal(err)
		}
		if err := os.Remove(path); err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(replacement, path); err != nil {
			t.Fatal(err)
		}
		if err := revalidateManagedArtifactSnapshot(path, snapshot); err == nil || !strings.Contains(err.Error(), "changed after install preflight") {
			t.Fatalf("replacement revalidation error = %v", err)
		}
	})

	t.Run("missing target appeared", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "config.json")
		snapshot, err := readManagedArtifactSnapshot(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("user edit"), 0o640); err != nil {
			t.Fatal(err)
		}
		if err := revalidateManagedArtifactSnapshot(path, snapshot); err == nil || !strings.Contains(err.Error(), "changed after install preflight") {
			t.Fatalf("appeared target revalidation error = %v", err)
		}
	})
}
