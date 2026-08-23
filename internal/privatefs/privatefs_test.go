package privatefs

import (
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestSecureDirectoryRejectsSymlinkAndIrregularTargets(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(root, "outside")
	if err := os.Mkdir(outside, 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "link")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	if err := SecureDirectory(link); err == nil {
		t.Fatal("directory symlink was accepted")
	}
	file := filepath.Join(root, "file")
	if err := os.WriteFile(file, []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := SecureDirectory(file); err == nil {
		t.Fatal("regular file was accepted as a directory")
	}
}

func TestValidateDirectoryEntryRejectsReplacement(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "private")
	if err := os.Mkdir(path, PrivateDirectoryMode); err != nil {
		t.Fatal(err)
	}
	opened, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	current, err := os.Stat(path)
	if err != nil || !os.SameFile(opened, current) {
		t.Fatalf("freeze private directory identity: %v", err)
	}
	if err := os.Rename(path, filepath.Join(root, "moved")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, PrivateDirectoryMode); err != nil {
		t.Fatal(err)
	}
	if err := validateDirectoryEntry(path, opened); err == nil {
		t.Fatal("replacement private directory entry was accepted")
	}
}

func TestOpenLockRepairsModeAndRejectsHardLinkAndSymlink(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "nested", "state.lock")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	file, err := OpenLock(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	assertPrivateLockSecurity(t, path)
	hardlink := filepath.Join(root, "hardlink.lock")
	if err := os.Link(path, hardlink); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenExistingLock(path); err == nil {
		t.Fatal("hard-linked private lock was accepted")
	}
	if err := os.Remove(hardlink); err != nil {
		t.Fatal(err)
	}
	symlink := filepath.Join(root, "symlink.lock")
	if err := os.Symlink(path, symlink); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenLock(symlink); err == nil {
		t.Fatal("lock symlink was accepted")
	}
}

func TestConcurrentFirstLockCreationPublishesOneValidIdentity(t *testing.T) {
	path := filepath.Join(t.TempDir(), "a", "b", "state.lock")
	const workers = 12
	results := make(chan error, workers)
	var group sync.WaitGroup
	for range workers {
		group.Add(1)
		go func() {
			defer group.Done()
			file, err := OpenLock(path)
			if err == nil {
				err = file.Close()
			}
			results <- err
		}()
	}
	group.Wait()
	close(results)
	for err := range results {
		if err != nil {
			t.Fatal(err)
		}
	}
	file, err := OpenExistingLock(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestOpenExistingLockReportsMissingTarget(t *testing.T) {
	_, err := OpenExistingLock(filepath.Join(t.TempDir(), "missing.lock"))
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing lock error = %v", err)
	}
}

func TestSecureFileDoesNotRequireOrModifyPrivateParent(t *testing.T) {
	parent := t.TempDir()
	before, err := os.Stat(parent)
	if err != nil {
		t.Fatal(err)
	}
	file, err := os.CreateTemp(parent, "transaction-*.private")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if err := SecureFile(file); err != nil {
		t.Fatal(err)
	}
	info, err := file.Stat()
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateFile(file, info); err != nil {
		t.Fatal(err)
	}
	after, err := os.Stat(parent)
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(before, after) || before.Mode() != after.Mode() {
		t.Fatalf("securing transaction file changed parent: before=%v after=%v", before.Mode(), after.Mode())
	}
}
