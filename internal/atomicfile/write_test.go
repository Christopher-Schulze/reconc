package atomicfile

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
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
	if err != nil || !written.Changed || written.Outcome != PublicationDurablyPublished {
		t.Fatalf("first write: written=%v err=%v", written, err)
	}
	written, err = WriteIfChanged(path, []byte("one\n"), 0o640)
	if err != nil || written.Changed || written.Outcome != PublicationNotPublished {
		t.Fatalf("identical write: written=%v err=%v", written, err)
	}
	written, err = WriteIfChanged(path, []byte("two\n"), 0o640)
	if err != nil || !written.Changed || written.Outcome != PublicationDurablyPublished {
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

func TestPublicationResultRetainsPostPublicationUncertainty(t *testing.T) {
	originalSync := syncParentDir
	t.Cleanup(func() { syncParentDir = originalSync })
	syncParentDir = func(*os.Root) error { return errors.New("injected parent sync failure") }

	path := filepath.Join(t.TempDir(), "state.json")
	result, err := WriteIfChanged(path, []byte("published\n"), 0o600)
	if err == nil || result.Outcome != PublicationPublishedUncertain || !result.Changed {
		t.Fatalf("replacement outcome = %+v err=%v", result, err)
	}
	if body, readErr := os.ReadFile(path); readErr != nil || string(body) != "published\n" {
		t.Fatalf("replacement body = %q err=%v", body, readErr)
	}

	newPath := filepath.Join(t.TempDir(), "new.json")
	result, err = WriteNew(newPath, []byte("new\n"), 0o600)
	if err == nil || result.Outcome != PublicationPublishedUncertain || !result.Changed {
		t.Fatalf("create-only outcome = %+v err=%v", result, err)
	}
	if body, readErr := os.ReadFile(newPath); readErr != nil || string(body) != "new\n" {
		t.Fatalf("create-only body = %q err=%v", body, readErr)
	}

	streamPath := filepath.Join(t.TempDir(), "stream.bin")
	if writeErr := os.WriteFile(streamPath, []byte("old\n"), 0o600); writeErr != nil {
		t.Fatal(writeErr)
	}
	result, err = WriteStream(streamPath, strings.NewReader("stream\n"), 32, 0o600)
	if err == nil || result.Outcome != PublicationPublishedUncertain || !result.Changed {
		t.Fatalf("stream outcome = %+v err=%v", result, err)
	}
	if body, readErr := os.ReadFile(streamPath); readErr != nil || string(body) != "stream\n" {
		t.Fatalf("stream body = %q err=%v", body, readErr)
	}
}

func TestWriteIfCurrentPublishesOnlyAuthorizedExistingState(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	before := []byte("before\n")
	if err := os.WriteFile(path, before, 0o640); err != nil {
		t.Fatal(err)
	}
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	expected := ExpectedCurrent{Data: before, Info: info, Exists: true}
	written, err := WriteIfCurrent(path, []byte("after\n"), 0o600, expected)
	if err != nil || !written.Changed {
		t.Fatalf("conditional write: written=%v err=%v", written, err)
	}
	body, err := os.ReadFile(path)
	if err != nil || string(body) != "after\n" {
		t.Fatalf("published bytes = %q, %v", body, err)
	}
}

func TestWriteIfCurrentRejectsConcurrentByteEdit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	before := []byte("before\n")
	if err := os.WriteFile(path, before, 0o600); err != nil {
		t.Fatal(err)
	}
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	expected := ExpectedCurrent{Data: before, Info: info, Exists: true}
	concurrent := []byte("edited\n")
	if err := os.WriteFile(path, concurrent, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, info.ModTime(), info.ModTime()); err != nil {
		t.Fatal(err)
	}
	if _, err := WriteIfCurrent(path, []byte("after!\n"), 0o600, expected); !errors.Is(err, ErrCurrentChanged) {
		t.Fatalf("concurrent byte edit error = %v", err)
	}
	body, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(body, concurrent) {
		t.Fatalf("concurrent bytes changed: body=%q err=%v", body, err)
	}
}

func TestWriteIfCurrentRejectsConcurrentIdentityReplacement(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	before := []byte("before\n")
	if err := os.WriteFile(path, before, 0o600); err != nil {
		t.Fatal(err)
	}
	info := openedFileInfo(t, path)
	expected := ExpectedCurrent{Data: before, Info: info, Exists: true}
	replaceWithDistinctIdentity(t, path, before, 0o600)
	if _, err := WriteIfCurrent(path, []byte("after!\n"), 0o600, expected); !errors.Is(err, ErrCurrentChanged) {
		t.Fatalf("concurrent replacement error = %v", err)
	}
	body, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(body, before) {
		t.Fatalf("replacement changed: body=%q err=%v", body, err)
	}
}

func TestWriteStreamIfCurrentPublishesOnlyAuthorizedExistingState(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.bin")
	before := []byte("before stream\n")
	if err := os.WriteFile(path, before, 0o600); err != nil {
		t.Fatal(err)
	}
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(before)
	result, err := WriteStreamIfCurrent(path, strings.NewReader("after stream\n"), 64, 0o755, ExpectedStream{
		Info: info, Digest: hex.EncodeToString(digest[:]), Exists: true,
	})
	if err != nil || !result.Changed || result.Outcome != PublicationDurablyPublished {
		t.Fatalf("conditional stream write: result=%v err=%v", result, err)
	}
	body, err := os.ReadFile(path)
	if err != nil || string(body) != "after stream\n" {
		t.Fatalf("stream bytes = %q err=%v", body, err)
	}
}

func TestWriteStreamIfCurrentRejectsConcurrentIdentityReplacement(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.bin")
	before := []byte("before stream\n")
	if err := os.WriteFile(path, before, 0o600); err != nil {
		t.Fatal(err)
	}
	info := openedFileInfo(t, path)
	digest := sha256.Sum256(before)
	replaceWithDistinctIdentity(t, path, before, 0o600)
	_, err := WriteStreamIfCurrent(path, strings.NewReader("after stream\n"), 64, 0o755, ExpectedStream{
		Info: info, Digest: hex.EncodeToString(digest[:]), Exists: true,
	})
	if !errors.Is(err, ErrCurrentChanged) {
		t.Fatalf("conditional stream replacement error = %v", err)
	}
	body, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(body, before) {
		t.Fatalf("replacement bytes changed: %q err=%v", body, err)
	}
}

func TestWriteIfCurrentMissingExpectationIsCreateOnly(t *testing.T) {
	t.Run("publishes missing target", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "nested", "state.json")
		written, err := WriteIfCurrent(path, []byte("created\n"), 0o600, ExpectedCurrent{})
		if err != nil || !written.Changed {
			t.Fatalf("create-only write: written=%v err=%v", written, err)
		}
		body, err := os.ReadFile(path)
		if err != nil || string(body) != "created\n" {
			t.Fatalf("created bytes = %q, %v", body, err)
		}
	})

	t.Run("rejects concurrent target", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "state.json")
		concurrent := []byte("concurrent\n")
		if err := os.WriteFile(path, concurrent, 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := WriteIfCurrent(path, []byte("created\n"), 0o600, ExpectedCurrent{}); !errors.Is(err, ErrCurrentChanged) {
			t.Fatalf("concurrent creation error = %v", err)
		}
		body, err := os.ReadFile(path)
		if err != nil || !bytes.Equal(body, concurrent) {
			t.Fatalf("concurrent creation changed: body=%q err=%v", body, err)
		}
	})
}

func TestWriteIfCurrentReconcilesAuthorizedModeWithoutReplacingBytes(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows does not expose POSIX permission bits")
	}
	path := filepath.Join(t.TempDir(), "state.json")
	before := []byte("same\n")
	if err := os.WriteFile(path, before, 0o644); err != nil {
		t.Fatal(err)
	}
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	expected := ExpectedCurrent{Data: before, Info: info, Exists: true}
	written, err := WriteIfCurrent(path, before, 0o600, expected)
	if err != nil || !written.Changed {
		t.Fatalf("conditional mode write: written=%v err=%v", written, err)
	}
	after, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(info, after) || after.Mode().Perm() != 0o600 {
		t.Fatalf("mode result: same=%v mode=%o", os.SameFile(info, after), after.Mode().Perm())
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
	if err != nil || !written.Changed {
		t.Fatalf("replace sparse file: written=%v err=%v", written, err)
	}
	body, err := os.ReadFile(path)
	if err != nil || string(body) != "small\n" {
		t.Fatalf("replacement = %q, %v", body, err)
	}
}

func TestWriteNewPublishesCompleteBytesAndRefusesExistingTarget(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "export.json")
	if _, err := WriteNew(path, []byte("first\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := WriteNew(path, []byte("second\n"), 0o600); err == nil {
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

func TestWriteNewPrePublicationFailuresRemoveTemporary(t *testing.T) {
	injected := errors.New("injected write-new boundary failure")
	for _, test := range []struct {
		name    string
		install func(*testing.T)
	}{
		{
			name: "file sync",
			install: func(t *testing.T) {
				original := syncWriteNewTemporary
				syncWriteNewTemporary = func(*os.File) error { return injected }
				t.Cleanup(func() { syncWriteNewTemporary = original })
			},
		},
		{
			name: "file close",
			install: func(t *testing.T) {
				original := closeWriteNewTemporary
				closeWriteNewTemporary = func(file *os.File) error {
					return errors.Join(file.Close(), injected)
				}
				t.Cleanup(func() { closeWriteNewTemporary = original })
			},
		},
		{
			name: "hardlink",
			install: func(t *testing.T) {
				original := linkWriteNewTemporary
				linkWriteNewTemporary = func(*os.Root, string, string) error { return injected }
				t.Cleanup(func() { linkWriteNewTemporary = original })
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			test.install(t)
			path := filepath.Join(t.TempDir(), "state.json")
			result, err := WriteNew(path, []byte("new\n"), 0o600)
			if !errors.Is(err, injected) || result.Changed || result.Outcome != PublicationNotPublished {
				t.Fatalf("result = %+v, err=%v", result, err)
			}
			if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("failed publication created target: %v", err)
			}
			assertNoWriteNewTemporary(t, path)
		})
	}
}

func TestWriteNewDirectorySyncFailuresCleanTemporary(t *testing.T) {
	injected := errors.New("injected post-link directory sync failure")
	for _, failCall := range []int{1, 2} {
		t.Run(fmt.Sprintf("call %d", failCall), func(t *testing.T) {
			original := syncParentDir
			calls := 0
			syncParentDir = func(*os.Root) error {
				calls++
				if calls == failCall {
					return injected
				}
				return nil
			}
			t.Cleanup(func() { syncParentDir = original })

			path := filepath.Join(t.TempDir(), "state.json")
			result, err := WriteNew(path, []byte("published\n"), 0o600)
			if !errors.Is(err, injected) || !result.Changed || result.Outcome != PublicationPublishedUncertain {
				t.Fatalf("result = %+v, err=%v", result, err)
			}
			if calls != 2 {
				t.Fatalf("directory sync calls = %d, want 2", calls)
			}
			if body, err := os.ReadFile(path); err != nil || string(body) != "published\n" {
				t.Fatalf("published body = %q, err=%v", body, err)
			}
			assertNoWriteNewTemporary(t, path)
		})
	}
}

func TestWriteNewRemoveFailureLeavesRecoverableTemporary(t *testing.T) {
	injected := errors.New("injected temporary removal failure")
	original := removeWriteNewTemporary
	calls := 0
	removeWriteNewTemporary = func(directory *os.Root, name string) error {
		calls++
		if calls == 1 {
			return injected
		}
		return original(directory, name)
	}
	t.Cleanup(func() { removeWriteNewTemporary = original })

	path := filepath.Join(t.TempDir(), "state.json")
	result, err := WriteNew(path, []byte("published\n"), 0o600)
	if !errors.Is(err, injected) || !result.Changed || result.Outcome != PublicationPublishedUncertain {
		t.Fatalf("result = %+v, err=%v", result, err)
	}
	matches, err := filepath.Glob(filepath.Join(filepath.Dir(path), ".state.json.*.tmp"))
	if err != nil || len(matches) != 1 {
		t.Fatalf("recoverable temporary = %v, err=%v", matches, err)
	}

	removeWriteNewTemporary = original
	if _, err := WriteNew(path, []byte("replacement\n"), 0o600); !errors.Is(err, ErrCurrentChanged) {
		t.Fatalf("recovery refusal error = %v", err)
	}
	assertNoWriteNewTemporary(t, path)
	if body, err := os.ReadFile(path); err != nil || string(body) != "published\n" {
		t.Fatalf("recovered target body = %q, err=%v", body, err)
	}
}

func TestWriteNewRecoversOnlyHardlinksToExactTarget(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "state.json")
	if err := os.WriteFile(path, []byte("published\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	residue := filepath.Join(root, ".state.json.00000000000000000000000000000001.tmp")
	if err := os.Link(path, residue); err != nil {
		t.Skipf("hardlinks unavailable: %v", err)
	}
	foreign := filepath.Join(root, ".state.json.00000000000000000000000000000002.tmp")
	if err := os.WriteFile(foreign, []byte("foreign\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := WriteNew(path, []byte("replacement\n"), 0o600); !errors.Is(err, ErrCurrentChanged) {
		t.Fatalf("existing-target refusal = %v", err)
	}
	if _, err := os.Lstat(residue); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("stale hardlink remains: %v", err)
	}
	if body, err := os.ReadFile(foreign); err != nil || string(body) != "foreign\n" {
		t.Fatalf("foreign temporary changed: body=%q err=%v", body, err)
	}
	if body, err := os.ReadFile(path); err != nil || string(body) != "published\n" {
		t.Fatalf("target changed: body=%q err=%v", body, err)
	}
}

func TestWriteNewRecoveryRejectsTemporaryIdentityReplacement(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "state.json")
	if err := os.WriteFile(path, []byte("published\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	residueName := ".state.json.00000000000000000000000000000001.tmp"
	residue := filepath.Join(root, residueName)
	if err := os.Link(path, residue); err != nil {
		t.Skipf("hardlinks unavailable: %v", err)
	}
	originalHook := beforeWriteNewRecoveryRemoval
	beforeWriteNewRecoveryRemoval = func(directory *os.Root, name string) error {
		if err := directory.Rename(name, name+".verified"); err != nil {
			return err
		}
		return directory.WriteFile(name, []byte("attacker\n"), 0o600)
	}
	t.Cleanup(func() { beforeWriteNewRecoveryRemoval = originalHook })

	if _, err := WriteNew(path, []byte("replacement\n"), 0o600); err == nil ||
		!strings.Contains(err.Error(), "changed identity during recovery") {
		t.Fatalf("replacement recovery error = %v", err)
	}
	if body, err := os.ReadFile(residue); err != nil || string(body) != "attacker\n" {
		t.Fatalf("replacement temporary changed: body=%q err=%v", body, err)
	}
	if body, err := os.ReadFile(residue + ".verified"); err != nil || string(body) != "published\n" {
		t.Fatalf("verified temporary changed: body=%q err=%v", body, err)
	}
	if body, err := os.ReadFile(path); err != nil || string(body) != "published\n" {
		t.Fatalf("target changed: body=%q err=%v", body, err)
	}
}

func assertNoWriteNewTemporary(t *testing.T, path string) {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(filepath.Dir(path), "."+filepath.Base(path)+".*.tmp"))
	if err != nil || len(matches) != 0 {
		t.Fatalf("write-new temporary residue = %v, err=%v", matches, err)
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
	replaceWithDistinctIdentity(t, path, []byte("replacement\n"), 0o640)
	if err := validateCurrent(parent.directory(), name, info); err == nil {
		t.Fatal("target identity swap was accepted")
	}
	body, err := os.ReadFile(path)
	if err != nil || string(body) != "replacement\n" {
		t.Fatalf("replacement target changed: body=%q err=%v", body, err)
	}
}

func replaceWithDistinctIdentity(t *testing.T, path string, data []byte, mode os.FileMode) {
	t.Helper()
	replacement := path + ".replacement"
	if err := os.WriteFile(replacement, data, mode); err != nil {
		t.Fatal(err)
	}
	originalInfo, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	replacementInfo, err := os.Lstat(replacement)
	if err != nil {
		t.Fatal(err)
	}
	if os.SameFile(originalInfo, replacementInfo) {
		t.Fatal("replacement unexpectedly shares the original identity")
	}
	if err := os.Rename(path, path+".old"); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(replacement, path); err != nil {
		t.Fatal(err)
	}
}

func openedFileInfo(t *testing.T, path string) os.FileInfo {
	t.Helper()
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	info, statErr := file.Stat()
	closeErr := file.Close()
	if statErr != nil {
		t.Fatal(statErr)
	}
	if closeErr != nil {
		t.Fatal(closeErr)
	}
	return info
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
