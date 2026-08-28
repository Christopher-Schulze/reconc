package atomicfile

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
)

func TestWriteIfChangedSkipsIdenticalPublication(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "state.json")
	written, err := WriteIfChanged(path, []byte("one\n"), 0o640)
	if err != nil || !written {
		t.Fatalf("first write: written=%v err=%v", written, err)
	}
	written, err = WriteIfChanged(path, []byte("one\n"), 0o640)
	if err != nil || written {
		t.Fatalf("identical write: written=%v err=%v", written, err)
	}
	written, err = WriteIfChanged(path, []byte("two\n"), 0o640)
	if err != nil || !written {
		t.Fatalf("changed write: written=%v err=%v", written, err)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "two\n" {
		t.Fatalf("published bytes: %q err=%v", data, err)
	}
	matches, err := filepath.Glob(filepath.Join(filepath.Dir(path), ".state.json.*.tmp"))
	if err != nil || len(matches) != 0 {
		t.Fatalf("atomic temp residue: %v err=%v", matches, err)
	}
}

func TestWriteIfChangedRejectsSymlinkTargets(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "target")
	link := filepath.Join(root, "state.json")
	if err := os.WriteFile(target, []byte("secret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if _, err := WriteIfChanged(link, []byte("replacement\n"), 0o600); err == nil {
		t.Fatal("symlink target must be rejected")
	}
	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "secret\n" {
		t.Fatalf("symlink target changed: %q", data)
	}
}

func TestWriteIfChangedReplacesDifferentSizedSparseFileWithoutWholeRead(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sparse")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := file.Truncate(64 << 20); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	written, err := WriteIfChanged(path, []byte("small\n"), 0o600)
	if err != nil || !written {
		t.Fatalf("replace sparse file: written=%v err=%v", written, err)
	}
	body, err := os.ReadFile(path)
	if err != nil || string(body) != "small\n" {
		t.Fatalf("replacement = %q, %v", body, err)
	}
}

func TestWriteNewPublishesCompleteBytesAndRefusesExistingTarget(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "export.json")
	if err := WriteNew(path, []byte("first\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := WriteNew(path, []byte("second\n"), 0o600); err == nil {
		t.Fatal("WriteNew replaced an existing target")
	}
	body, err := os.ReadFile(path)
	if err != nil || string(body) != "first\n" {
		t.Fatalf("published bytes = %q, %v", body, err)
	}
	matches, err := filepath.Glob(filepath.Join(filepath.Dir(path), ".export.json.*.tmp"))
	if err != nil || len(matches) != 0 {
		t.Fatalf("new-file temporary residue = %v, %v", matches, err)
	}
}

func TestSecureExistingIfMatchesNeverReplacesContent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "backup")
	if err := os.WriteFile(path, []byte("preserved\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	matched, err := SecureExistingIfMatches(path, []byte("different\n"), 0o600)
	if err != nil || matched {
		t.Fatalf("different content matched: matched=%v err=%v", matched, err)
	}
	body, err := os.ReadFile(path)
	if err != nil || string(body) != "preserved\n" {
		t.Fatalf("different content changed: body=%q err=%v", body, err)
	}
	matched, err = SecureExistingIfMatches(path, body, 0o600)
	if err != nil || !matched {
		t.Fatalf("identical content did not match: matched=%v err=%v", matched, err)
	}
	if runtime.GOOS != "windows" {
		info, statErr := os.Stat(path)
		if statErr != nil {
			t.Fatal(statErr)
		}
		if info.Mode().Perm() != 0o600 {
			t.Fatalf("secured mode = %o, want 600", info.Mode().Perm())
		}
	}
}

func TestPublicationCreatesExplicitPublicAndPrivateParents(t *testing.T) {
	root := t.TempDir()
	public := filepath.Join(root, "public", "nested", "state")
	if _, err := WriteIfChanged(public, []byte("public\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	private := filepath.Join(root, "private", "nested", "state")
	if _, err := WritePrivateIfChanged(private, []byte("private\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	assertPublicationParent(t, filepath.Dir(public), PublicParentMode)
	assertPublicationParent(t, filepath.Dir(private), PrivateParentMode)
}

func TestPublicationRejectsNestedParentSymlink(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	link := filepath.Join(root, "nested")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	path := filepath.Join(link, "state.json")
	if _, err := WriteIfChanged(path, []byte("blocked\n"), 0o600); err == nil {
		t.Fatal("nested parent symlink was accepted")
	}
	if _, err := os.Lstat(filepath.Join(outside, "state.json")); !os.IsNotExist(err) {
		t.Fatalf("publication escaped through parent symlink: %v", err)
	}
}

func TestBoundParentDetectsDirectoryIdentitySwap(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "nested", "state.json")
	parent, _, err := bindParent(path, PublicParentMode)
	if err != nil {
		t.Fatal(err)
	}
	defer parent.close()
	original := filepath.Join(root, "nested")
	moved := filepath.Join(root, "moved")
	if err := os.Rename(original, moved); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(original, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := parent.validate(); err == nil {
		t.Fatal("parent identity swap was accepted")
	}
}

func TestTargetIdentitySwapIsRejectedWithoutTouchingReplacement(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "state.json")
	if err := os.WriteFile(path, []byte("before\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	parent, name, err := bindParent(path, PublicParentMode)
	if err != nil {
		t.Fatal(err)
	}
	defer parent.close()
	file, info, err := openCurrent(parent.directory(), name, path)
	if err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(path, path+".old"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("replacement\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := validateCurrent(parent.directory(), name, info); err == nil {
		t.Fatal("target identity swap was accepted")
	}
	body, err := os.ReadFile(path)
	if err != nil || string(body) != "replacement\n" {
		t.Fatalf("replacement target changed: body=%q err=%v", body, err)
	}
}

func TestConcurrentPublicationsRemainWholeAndLeaveNoTemporaries(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "state.json")
	const writers = 24
	var wait sync.WaitGroup
	errorsByWriter := make(chan error, writers)
	for index := range writers {
		wait.Add(1)
		go func() {
			defer wait.Done()
			body := []byte(fmt.Sprintf("writer-%02d\n", index))
			_, err := WriteIfChanged(path, body, 0o600)
			errorsByWriter <- err
		}()
	}
	wait.Wait()
	close(errorsByWriter)
	succeeded := 0
	for err := range errorsByWriter {
		if err == nil {
			succeeded++
			continue
		}
		if !strings.Contains(err.Error(), "changed") {
			t.Fatalf("unexpected concurrent publication error: %v", err)
		}
	}
	if succeeded == 0 {
		t.Fatal("no concurrent publication succeeded")
	}
	body, err := os.ReadFile(path)
	if err != nil || !strings.HasPrefix(string(body), "writer-") || len(body) != len("writer-00\n") {
		t.Fatalf("published body is torn: %q err=%v", body, err)
	}
	matches, err := filepath.Glob(filepath.Join(filepath.Dir(path), ".state.json.*.tmp"))
	if err != nil || len(matches) != 0 {
		t.Fatalf("concurrent temporary residue = %v, %v", matches, err)
	}
}

func TestReplacementFailureRemovesPreparedTemporary(t *testing.T) {
	root := t.TempDir()
	directory, err := os.OpenRoot(root)
	if err != nil {
		t.Fatal(err)
	}
	defer directory.Close()
	if err := os.Mkdir(filepath.Join(root, "target"), 0o755); err != nil {
		t.Fatal(err)
	}
	file, temporary, err := createTemporary(directory, "target")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.Write([]byte("prepared\n")); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	if err := replaceTemporary(directory, temporary, "target"); err == nil {
		t.Fatal("replacement over a directory unexpectedly succeeded")
	}
	if _, err := directory.Lstat(temporary); !os.IsNotExist(err) {
		t.Fatalf("failed replacement left temporary: %v", err)
	}
}
