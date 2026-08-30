package privatefs

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"reconc.dev/reconc/internal/filelock"
)

type readOnlyLockMetadata struct {
	mode    os.FileMode
	size    int64
	modTime time.Time
}

func TestOpenExistingLockReadOnlySupportsSharedLockWithoutMutation(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "private")
	if err := RepairDirectory(directory); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(directory, "state.lock")
	file, err := OpenLock(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	before := readOnlyLockMetadataForTest(t, path)

	file, err = OpenExistingLockReadOnly(path)
	if err != nil {
		t.Fatal(err)
	}
	unlock, err := filelock.TryRLock(file)
	if err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	if err := unlock(); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	if after := readOnlyLockMetadataForTest(t, path); !reflect.DeepEqual(after, before) {
		t.Fatalf("read-only shared lock changed metadata: before=%#v after=%#v", before, after)
	}
}

func readOnlyLockMetadataForTest(t testing.TB, path string) readOnlyLockMetadata {
	t.Helper()
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	return readOnlyLockMetadata{mode: info.Mode(), size: info.Size(), modTime: info.ModTime()}
}
