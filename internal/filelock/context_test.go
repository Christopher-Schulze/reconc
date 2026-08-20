package filelock

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLockContextCancelsAndTimesOut(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.lock")
	first, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	unlock, err := Lock(first)
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	second, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := LockContext(canceled, second, time.Second); !errors.Is(err, ErrLockCanceled) || !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled acquisition = %v", err)
	}
	started := time.Now()
	if _, err := LockContext(context.Background(), second, 30*time.Millisecond); !errors.Is(err, ErrLockTimeout) {
		t.Fatalf("timeout acquisition = %v", err)
	}
	if elapsed := time.Since(started); elapsed < 20*time.Millisecond || elapsed > time.Second {
		t.Fatalf("timeout elapsed = %s", elapsed)
	}
}

func TestLockContextSucceedsAfterReleaseAndPropagatesUnlockError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.lock")
	first, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	unlockFirst, err := Lock(first)
	if err != nil {
		t.Fatal(err)
	}
	second, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	result := make(chan error, 1)
	go func() {
		unlock, lockErr := LockContext(context.Background(), second, time.Second)
		if lockErr != nil {
			result <- lockErr
			return
		}
		result <- unlock()
	}()
	time.Sleep(15 * time.Millisecond)
	if err := unlockFirst(); err != nil {
		t.Fatal(err)
	}
	if err := <-result; err != nil {
		t.Fatal(err)
	}
	third, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	unlockThird, err := LockContext(context.Background(), third, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if err := third.Close(); err != nil {
		t.Fatal(err)
	}
	if err := unlockThird(); err == nil {
		t.Fatal("unlock of a closed descriptor succeeded")
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := LockContext(context.Background(), first, 10*time.Millisecond); err == nil {
		t.Fatal("closed descriptor accepted")
	}
}
