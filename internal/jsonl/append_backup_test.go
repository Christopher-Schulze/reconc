package jsonl

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestCreateAppendBackupRejectsSourceReplacement(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("replacing an open source is not reliable on Windows")
	}
	root := t.TempDir()
	path := filepath.Join(root, "events.jsonl")
	source := archivePath(path, 1)
	moved := source + ".moved"
	original := []byte("original archive\n")
	replacement := []byte("replacement archive\n")
	if err := os.WriteFile(source, original, 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := createAppendBackupWithLayoutHooks(path, defaultLayout(path), 1, 64, appendBackupHooks{
		beforePublish: func() error {
			if err := os.Rename(source, moved); err != nil {
				return err
			}
			return os.WriteFile(source, replacement, 0o644)
		},
	})
	if err == nil {
		t.Fatal("source replacement was accepted")
	}
	if _, statErr := os.Lstat(appendBackupPath(path, 1)); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("rejected source replacement left a backup: %v", statErr)
	}
	assertAppendBackupBytes(t, moved, original)
	assertAppendBackupBytes(t, source, replacement)
}

func TestOpenAppendBackupSourceRejectsSecurityReplacement(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("replacing an open source is not reliable on Windows")
	}
	for _, test := range []struct {
		name    string
		symlink bool
	}{
		{name: "regular replacement"},
		{name: "symlink substitution", symlink: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			path := filepath.Join(root, "events.jsonl")
			source := archivePath(path, 1)
			if err := os.WriteFile(source, []byte("archive\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			security := &replacingLayoutSecurity{
				path: source, replacement: []byte("replacement\n"), symlink: test.symlink,
			}
			if test.symlink {
				security.target = filepath.Join(root, "foreign.jsonl")
				if err := os.WriteFile(security.target, []byte("foreign\n"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			before, err := os.Lstat(source)
			if err != nil {
				t.Fatal(err)
			}
			layout := defaultLayout(path)
			layout.Security = security
			file, _, data, _, err := openAppendBackupSource(source, before, layout, 64)
			if file != nil {
				_ = file.Close()
			}
			if err == nil {
				t.Fatal("security-validated replacement was accepted")
			}
			if data != nil {
				t.Fatalf("rejected replacement returned bytes: %q", data)
			}
		})
	}
}

func TestCreateAppendBackupRejectsHardLinkCountChange(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "events.jsonl")
	source := archivePath(path, 1)
	victim := filepath.Join(root, "source-alias")
	want := []byte("archive\n")
	if err := os.WriteFile(source, want, 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := createAppendBackupWithLayoutHooks(path, defaultLayout(path), 1, 64, appendBackupHooks{
		beforePublish: func() error { return os.Link(source, victim) },
	})
	if err == nil {
		t.Fatal("source hard-link count change was accepted")
	}
	if _, statErr := os.Lstat(appendBackupPath(path, 1)); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("rejected hard-link count change left a backup: %v", statErr)
	}
	assertAppendBackupBytes(t, source, want)
	assertAppendBackupBytes(t, victim, want)
}

func TestCreateAppendBackupRejectsExistingCollision(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "events.jsonl")
	source := archivePath(path, 1)
	backupPath := appendBackupPath(path, 1)
	sourceBody := []byte("archive\n")
	collisionBody := []byte("unrelated\n")
	if err := os.WriteFile(source, sourceBody, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(backupPath, collisionBody, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := createAppendBackupWithLayout(path, defaultLayout(path), 1, 64); err == nil {
		t.Fatal("existing backup collision was accepted")
	}
	assertAppendBackupBytes(t, source, sourceBody)
	assertAppendBackupBytes(t, backupPath, collisionBody)
}

func TestBeginAppendJournalPreservesExistingBackupCollision(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "events.jsonl")
	backupPath := appendBackupPath(path, 1)
	collisionBody := []byte("unrelated\n")
	if err := os.WriteFile(backupPath, collisionBody, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := beginAppendJournal(path, Policy{MaxBytes: 64, MaxArchives: 1}, true, true); err == nil {
		t.Fatal("begin append accepted an existing backup collision")
	}
	assertAppendBackupBytes(t, backupPath, collisionBody)
	if _, err := os.Lstat(appendJournalPath(path)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("collision preflight left a journal: %v", err)
	}
}

func TestCreateAppendBackupLinksValidatedSource(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "events.jsonl")
	source := archivePath(path, 1)
	backupPath := appendBackupPath(path, 1)
	want := []byte("archive\n")
	if err := os.WriteFile(source, want, 0o644); err != nil {
		t.Fatal(err)
	}
	backup, err := createAppendBackupWithLayout(path, defaultLayout(path), 1, 64)
	if err != nil {
		t.Fatal(err)
	}
	if !backup.Existed || backup.Digest == "" {
		t.Fatalf("backup metadata = %+v", backup)
	}
	sourceInfo, err := os.Lstat(source)
	if err != nil {
		t.Fatal(err)
	}
	backupInfo, err := os.Lstat(backupPath)
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(sourceInfo, backupInfo) {
		t.Fatalf("backup is not linked to the validated source: source=%v backup=%v", sourceInfo, backupInfo)
	}
	assertAppendBackupBytes(t, backupPath, want)
}

func assertAppendBackupBytes(t *testing.T, path string, want []byte) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("%s = %q, want %q", path, got, want)
	}
}
