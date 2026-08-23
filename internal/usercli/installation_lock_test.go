package usercli

import (
	"os"
	"strings"
	"testing"
	"time"
)

func TestInstallationPurgePreservesOneLockIdentity(t *testing.T) {
	t.Setenv("RECONC_HOME", t.TempDir())
	paths, err := resolveReceiptPaths()
	if err != nil {
		t.Fatal(err)
	}
	if err := withReceiptLock(paths, func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	before, err := os.Lstat(paths.lock)
	if err != nil {
		t.Fatal(err)
	}
	if err := withReceiptLock(paths, func() error { return purgeInstallationState(paths) }); err != nil {
		t.Fatal(err)
	}
	after, err := os.Lstat(paths.lock)
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(before, after) {
		t.Fatal("purge replaced the installation coordination lock")
	}
}

func TestReceiptReadReportsConcurrentReceiptChangeWithoutRepeatingOperation(t *testing.T) {
	t.Setenv("RECONC_HOME", t.TempDir())
	paths, err := resolveReceiptPaths()
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	err = withReceiptReadLock(paths, func() error {
		calls++
		return withReceiptLock(paths, func() error {
			return os.WriteFile(paths.receipt, []byte("changed"), 0o600)
		})
	})
	if err == nil || !strings.Contains(err.Error(), "changed during unlocked read") {
		t.Fatalf("concurrent receipt error = %v", err)
	}
	if calls != 1 {
		t.Fatalf("read operation ran %d times, want exactly one execution", calls)
	}
}

func TestInstallationReadersAndWritersSerializeAcrossPurge(t *testing.T) {
	t.Setenv("RECONC_HOME", t.TempDir())
	paths, err := resolveReceiptPaths()
	if err != nil {
		t.Fatal(err)
	}
	entered := make(chan struct{})
	release := make(chan struct{})
	writerDone := make(chan error, 1)
	go func() {
		writerDone <- withReceiptLock(paths, func() error {
			close(entered)
			<-release
			return purgeInstallationState(paths)
		})
	}()
	<-entered

	readerEntered := make(chan struct{})
	readerDone := make(chan error, 1)
	go func() {
		readerDone <- withReceiptReadLock(paths, func() error {
			close(readerEntered)
			return nil
		})
	}()
	select {
	case <-readerEntered:
		t.Fatal("read-only doctor path bypassed the installation writer lock")
	case <-time.After(50 * time.Millisecond):
	}
	close(release)
	if err := <-writerDone; err != nil {
		t.Fatal(err)
	}
	select {
	case <-readerEntered:
	case <-time.After(time.Second):
		t.Fatal("read-only doctor path did not resume after purge")
	}
	if err := <-readerDone; err != nil {
		t.Fatal(err)
	}
}

func TestReceiptReadLockDoesNotCreateMissingState(t *testing.T) {
	t.Setenv("RECONC_HOME", t.TempDir())
	paths, err := resolveReceiptPaths()
	if err != nil {
		t.Fatal(err)
	}
	called := false
	if err := withReceiptReadLock(paths, func() error {
		called = true
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Fatal("read-only operation was not called")
	}
	if _, err := os.Lstat(paths.directory); !os.IsNotExist(err) {
		t.Fatalf("read-only lock created installation state: %v", err)
	}
}

func TestReceiptReadRevalidatesWithoutRepeatingOperationWhenInstallationStarts(t *testing.T) {
	t.Setenv("RECONC_HOME", t.TempDir())
	paths, err := resolveReceiptPaths()
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	if err := withReceiptReadLock(paths, func() error {
		calls++
		return withReceiptLock(paths, func() error { return nil })
	}); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("read operation ran %d times, want exactly one execution", calls)
	}
}
