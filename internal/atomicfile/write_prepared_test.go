package atomicfile

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestWritePreparedIfChangedSecuresTemporaryBeforePayload(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "shared", "private.json")
	prepared := ""
	result, err := WritePreparedIfChanged(target, []byte("secret\n"), 0o600, func(path string) error {
		prepared = path
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		if info.Size() != 0 {
			return errors.New("payload was written before preparation")
		}
		return os.Chmod(path, 0o600)
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Changed || result.Outcome != PublicationDurablyPublished {
		t.Fatalf("publication result = %+v", result)
	}
	if prepared == "" || prepared == target {
		t.Fatalf("preparation path = %q, want unpublished temporary", prepared)
	}
	if body, err := os.ReadFile(target); err != nil || string(body) != "secret\n" {
		t.Fatalf("published body = %q, err=%v", body, err)
	}
}

func TestWritePreparedIfChangedPreparationFailurePublishesNothing(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "shared", "private.json")
	want := errors.New("reject preparation")
	if _, err := WritePreparedIfChanged(target, []byte("secret\n"), 0o600, func(string) error {
		return want
	}); !errors.Is(err, want) {
		t.Fatalf("preparation error = %v, want %v", err, want)
	}
	if _, err := os.Lstat(target); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("target published after preparation failure: %v", err)
	}
	entries, err := os.ReadDir(filepath.Dir(target))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("preparation failure left %d temporary files", len(entries))
	}
}
