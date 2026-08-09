package main

import (
	"errors"
	"os"
	"path/filepath"
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

func TestWriteAtomicRejectsMissingTargetWithoutCreatingIt(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "new.md")
	if err := writeAtomic(root, path, []byte("content\n"), nil); err == nil {
		t.Fatal("writeAtomic accepted a missing transaction target")
	}
	if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing target was created, err=%v", err)
	}
	matches, err := filepath.Glob(filepath.Join(filepath.Dir(path), ".promote-task-done-*"))
	if err != nil {
		t.Fatalf("glob temporary files: %v", err)
	}
	if len(matches) != 0 {
		t.Fatalf("atomic write leaked temporary files: %#v", matches)
	}
}
