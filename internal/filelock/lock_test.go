package filelock

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLockSerializesAccess(t *testing.T) {
	path := filepath.Join(t.TempDir(), "retention.lock")
	first, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	second, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()

	unlockFirst, err := Lock(first)
	if err != nil {
		t.Fatal(err)
	}
	acquired := make(chan func() error, 1)
	errs := make(chan error, 1)
	go func() {
		unlock, lockErr := Lock(second)
		if lockErr != nil {
			errs <- lockErr
			return
		}
		acquired <- unlock
	}()

	select {
	case unlock := <-acquired:
		_ = unlock()
		t.Fatal("second lock acquired before first lock was released")
	case err := <-errs:
		t.Fatalf("second lock failed: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	if err := unlockFirst(); err != nil {
		t.Fatal(err)
	}
	select {
	case unlock := <-acquired:
		if err := unlock(); err != nil {
			t.Fatal(err)
		}
	case err := <-errs:
		t.Fatalf("second lock failed: %v", err)
	case <-time.After(2 * time.Second):
		t.Fatal("second lock did not acquire after release")
	}
}

func TestTryLockRejectsContendedFileWithoutWaiting(t *testing.T) {
	path := filepath.Join(t.TempDir(), "retention.lock")
	first, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	second, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()

	unlockFirst, err := TryLock(first)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := TryLock(second); err == nil {
		t.Fatal("contended TryLock unexpectedly succeeded")
	} else if !IsContended(err) {
		t.Fatalf("contended TryLock error was not classified as contention: %v", err)
	}
	if err := unlockFirst(); err != nil {
		t.Fatal(err)
	}
	unlockSecond, err := TryLock(second)
	if err != nil {
		t.Fatalf("TryLock did not recover after release: %v", err)
	}
	if err := unlockSecond(); err != nil {
		t.Fatal(err)
	}
}

func TestIsContendedRejectsPermanentLockError(t *testing.T) {
	file, err := os.CreateTemp(t.TempDir(), "closed-lock")
	if err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := TryLock(file); err == nil {
		t.Fatal("TryLock unexpectedly accepted a closed file")
	} else if IsContended(err) {
		t.Fatalf("closed-file lock error was misclassified as contention: %v", err)
	}
}

func TestSharedLocksCoexistAndExcludeWriter(t *testing.T) {
	path := filepath.Join(t.TempDir(), "shared.lock")
	first, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	second, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	writer, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()

	unlockFirst, err := TryRLock(first)
	if err != nil {
		t.Fatal(err)
	}
	unlockSecond, err := TryRLock(second)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := TryLock(writer); err == nil {
		t.Fatal("exclusive lock entered while shared leases were active")
	}
	if err := unlockFirst(); err != nil {
		t.Fatal(err)
	}
	if err := unlockSecond(); err != nil {
		t.Fatal(err)
	}
	unlockWriter, err := TryLock(writer)
	if err != nil {
		t.Fatalf("exclusive lock did not acquire after shared leases closed: %v", err)
	}
	if err := unlockWriter(); err != nil {
		t.Fatal(err)
	}
}
