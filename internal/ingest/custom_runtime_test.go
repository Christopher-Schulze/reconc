package ingest

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestLoadCustomRuntimeSourcesRejectsSymlinkAndOversize(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	directory := filepath.Join(root, ".reconc", "runtimes")
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "runtime.json")
	if err := os.WriteFile(outside, []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(directory, "escape.json")); err != nil {
		if runtime.GOOS == "windows" {
			t.Skip("symlinks unavailable")
		}
		t.Fatal(err)
	}
	if _, err := LoadCustomRuntimeSources(root); err == nil || !strings.Contains(err.Error(), "non-symlink regular file") {
		t.Fatalf("symlink error = %v", err)
	}
	if err := os.Remove(filepath.Join(directory, "escape.json")); err != nil {
		t.Fatal(err)
	}
	sparse := filepath.Join(directory, "huge.json")
	file, err := os.Create(sparse)
	if err != nil {
		t.Fatal(err)
	}
	if err := file.Truncate(256<<10 + 1); err != nil {
		file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadCustomRuntimeSources(root); err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("oversize error = %v", err)
	}
}
