//go:build !windows

package jsonl

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func assertJSONLFileMode(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != want.Perm() {
		t.Fatalf("JSONL mode = %v, want %04o, err = %v", info, want.Perm(), err)
	}
}

func TestDefaultLayoutRejectsGroupWorldWritableObjects(t *testing.T) {
	for _, test := range []struct {
		name string
		run  func(*testing.T, string) (string, error)
	}{
		{name: "live", run: func(t *testing.T, path string) (string, error) {
			writeModeFixture(t, path, []byte("live\n"), 0o666)
			return path, Append(path, []byte("next"), Policy{MaxBytes: 64, MaxArchives: 1})
		}},
		{name: "lock", run: func(t *testing.T, path string) (string, error) {
			lockPath := defaultLayout(path).LockPath
			writeModeFixture(t, lockPath, nil, 0o666)
			return lockPath, Append(path, []byte("next"), Policy{MaxBytes: 64, MaxArchives: 1})
		}},
		{name: "archive", run: func(t *testing.T, path string) (string, error) {
			writeModeFixture(t, path, []byte("current\n"), 0o644)
			archive := path + ".1"
			writeModeFixture(t, archive, []byte("old\n"), 0o666)
			return archive, Append(path, []byte("next"), Policy{MaxBytes: 9, MaxArchives: 1})
		}},
		{name: "backup", run: func(t *testing.T, path string) (string, error) {
			writeModeFixture(t, path, []byte("current\n"), 0o644)
			backup := appendBackupPathWithLayout(defaultLayout(path), 0)
			writeModeFixture(t, backup, []byte("residue\n"), 0o666)
			return backup, Append(path, []byte("next"), Policy{MaxBytes: 9, MaxArchives: 1})
		}},
		{name: "journal", run: func(t *testing.T, path string) (string, error) {
			layout := defaultLayout(path)
			if err := withLayoutLock(path, layout, func() error {
				_, err := beginAppendJournalWithLayout(path, Policy{MaxBytes: 64, MaxArchives: 1}, layout, false, true)
				return err
			}); err != nil {
				t.Fatal(err)
			}
			if err := os.Chmod(layout.JournalPath, 0o666); err != nil {
				t.Fatal(err)
			}
			return layout.JournalPath, Recover(path, func() error { return nil })
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "events.jsonl")
			objectPath, err := test.run(t, path)
			if err == nil || !strings.Contains(err.Error(), "group/world-writable") {
				t.Fatalf("insecure %s error = %v", test.name, err)
			}
			info, statErr := os.Lstat(objectPath)
			if statErr != nil || info.Mode().Perm() != 0o666 {
				t.Fatalf("rejected %s was repaired or removed: info=%v err=%v", test.name, info, statErr)
			}
		})
	}
}

func TestDefaultAppendPreservesRestrictiveLockMode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.jsonl")
	lockPath := defaultLayout(path).LockPath
	writeModeFixture(t, lockPath, nil, 0o600)
	if err := Append(path, []byte("record"), Policy{MaxBytes: 64, MaxArchives: 1}); err != nil {
		t.Fatal(err)
	}
	assertJSONLFileMode(t, lockPath, 0o600)
}

func TestRemoveArchiveRequiresLeaseAndRejectsReplacement(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "events.jsonl")
	archive := path + ".1"
	writeModeFixture(t, archive, []byte("original\n"), 0o600)
	info, err := os.Lstat(archive)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := RemoveArchiveWithLayout(path, 1, info, defaultLayout(path)); err == nil {
		t.Fatal("archive removal without a maintenance lease succeeded")
	}
	err = WithLayoutMaintenanceContext(t.Context(), path, defaultLayout(path), nil, func(layout Layout) error {
		moved := archive + ".moved"
		if err := os.Rename(archive, moved); err != nil {
			return err
		}
		if err := os.WriteFile(archive, []byte("replacement\n"), 0o600); err != nil {
			return err
		}
		_, removeErr := RemoveArchiveWithLayout(path, 1, info, layout)
		return removeErr
	})
	if err == nil || !strings.Contains(err.Error(), "changed identity") {
		t.Fatalf("replacement removal error = %v", err)
	}
	for _, candidate := range []string{archive, archive + ".moved"} {
		if _, err := os.Lstat(candidate); err != nil {
			t.Fatalf("replacement test lost %s: %v", candidate, err)
		}
	}
}

func writeModeFixture(t *testing.T, path string, body []byte, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, body, mode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, mode); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Lstat(path); err != nil || info.Mode().Perm() != mode.Perm() {
		t.Fatalf("fixture mode = %v, want %o, err=%v", info, mode.Perm(), err)
	}
}
