package main

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestWrapRollbackErrorPreservesNilAndCause(t *testing.T) {
	if wrapRollbackError(nil) != nil {
		t.Fatal("nil rollback error must remain nil")
	}
	cause := errors.New("restore failed")
	got := wrapRollbackError(cause)
	if !errors.Is(got, cause) || !strings.Contains(got.Error(), "rollback failed") {
		t.Fatalf("wrapRollbackError() = %v", got)
	}
}

func TestWriteAtomicCreatesNewFileWithPrivateStableMode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "new.md")
	if err := writeAtomic(path, []byte("content\n")); err != nil {
		t.Fatalf("writeAtomic: %v", err)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read result: %v", err)
	}
	if string(body) != "content\n" {
		t.Fatalf("content = %q", body)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat result: %v", err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o644 {
		t.Fatalf("new-file mode = %o, want 644", info.Mode().Perm())
	}
	if !info.Mode().IsRegular() {
		t.Fatalf("new-file mode = %s, want regular file", info.Mode())
	}
	matches, err := filepath.Glob(filepath.Join(filepath.Dir(path), ".promote-task-done-*"))
	if err != nil {
		t.Fatalf("glob temporary files: %v", err)
	}
	if len(matches) != 0 {
		t.Fatalf("atomic write leaked temporary files: %#v", matches)
	}
}
