package actionstate

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

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
