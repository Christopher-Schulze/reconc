package actionstate

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

type existingFileMetadata struct {
	mode    os.FileMode
	size    int64
	modTime time.Time
}

func TestExistingIdentityAndProjectStorageNeverCreateMissingState(t *testing.T) {
	base := t.TempDir()
	home := filepath.Join(base, "missing-home")
	if _, err := AcquireExistingIdentityKey(context.Background(), home); err == nil || !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("AcquireExistingIdentityKey() error = %v", err)
	}
	if _, err := os.Lstat(home); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("read-only identity acquisition created %s: %v", home, err)
	}
}

func TestExistingProjectStorageMatchesWriterStorage(t *testing.T) {
	home := filepath.Join(t.TempDir(), "reconc-home")
	repository := t.TempDir()
	if _, err := CreateIdentityKey(home, time.Date(2026, 8, 11, 12, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	writerLease, err := AcquireIdentityKey(context.Background(), home)
	if err != nil {
		t.Fatal(err)
	}
	writerStore, err := OpenStore(StoreOptions{
		Home: home, Repository: repository, KeyLease: writerLease, OwnerID: "existing-storage-writer",
	})
	if err != nil {
		t.Fatal(err)
	}
	written, err := writerStore.PrivateProjectStorage()
	if err != nil {
		t.Fatal(err)
	}
	if err := writerLease.Close(); err != nil {
		t.Fatal(err)
	}
	readerLease, err := AcquireExistingIdentityKey(context.Background(), home)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := readerLease.Close(); err != nil {
			t.Errorf("close reader lease: %v", err)
		}
	})
	read, err := OpenExistingPrivateProjectStorage(home, repository, readerLease)
	if err != nil {
		t.Fatal(err)
	}
	if read.ActionDirectory() != written.ActionDirectory() ||
		read.RepositoryIdentity() != written.RepositoryIdentity() ||
		read.ProjectKey() != written.ProjectKey() {
		t.Fatal("read-only and writer storage bindings differ")
	}
}

func TestAcquireExistingIdentityKeyPreservesExistingFilesAndCancellation(t *testing.T) {
	home := filepath.Join(t.TempDir(), "reconc-home")
	if _, err := CreateIdentityKey(home, time.Date(2026, 8, 11, 12, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	paths := []string{
		home,
		filepath.Join(home, "action"),
		filepath.Join(home, "action", "identity-key.lock"),
		filepath.Join(home, "action", "identity-key.json"),
	}
	before := existingMetadataForTest(t, paths)
	lease, err := AcquireExistingIdentityKey(context.Background(), home)
	if err != nil {
		t.Fatal(err)
	}
	if err := lease.Close(); err != nil {
		t.Fatal(err)
	}
	if after := existingMetadataForTest(t, paths); !reflect.DeepEqual(after, before) {
		t.Fatalf("existing identity-key read changed metadata: before=%#v after=%#v", before, after)
	}

	writer, err := acquireFileLock(context.Background(), filepath.Join(home, "action", "identity-key.lock"), StateLockTimeout)
	if err != nil {
		t.Fatal(err)
	}
	contentionBefore := existingMetadataForTest(t, paths)
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := AcquireExistingIdentityKey(canceled, home); !errors.Is(err, context.Canceled) {
		_ = writer.close()
		t.Fatalf("canceled existing identity-key acquisition = %v", err)
	}
	if err := writer.close(); err != nil {
		t.Fatal(err)
	}
	if after := existingMetadataForTest(t, paths); !reflect.DeepEqual(after, contentionBefore) {
		t.Fatalf("canceled identity-key read changed metadata: before=%#v after=%#v", contentionBefore, after)
	}
}

func existingMetadataForTest(t testing.TB, paths []string) []existingFileMetadata {
	t.Helper()
	result := make([]existingFileMetadata, 0, len(paths))
	for _, path := range paths {
		info, err := os.Lstat(path)
		if err != nil {
			t.Fatal(err)
		}
		result = append(result, existingFileMetadata{mode: info.Mode(), size: info.Size(), modTime: info.ModTime()})
	}
	return result
}
