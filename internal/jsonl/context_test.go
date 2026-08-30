package jsonl

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"testing/synctest"
	"time"

	"reconc.dev/reconc/internal/boundedio"
	"reconc.dev/reconc/internal/filelock"
)

func TestDefaultLayoutUsesBoundedLockPolicy(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.jsonl")
	timeout := defaultLayout(path).LockTimeout
	if timeout != DefaultLockTimeout {
		t.Fatalf("default layout timeout = %s, want %s", timeout, DefaultLockTimeout)
	}
	if DefaultLockTimeout != filelock.DefaultTimeout {
		t.Fatalf("JSONL default timeout = %s, want shared default %s", DefaultLockTimeout, filelock.DefaultTimeout)
	}
}

func TestDefaultRecoveryAcceptsLegacyUnboundedLayoutIdentity(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.jsonl")
	legacy := defaultLayout(path)
	legacy.LockTimeout = 0
	if err := withLayoutLock(path, legacy, func() error {
		_, err := beginAppendJournalWithLayout(path, Policy{MaxBytes: 64, MaxArchives: 1}, legacy, false, true)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	commits := 0
	if err := Recover(path, func() error {
		commits++
		return nil
	}); err != nil {
		t.Fatalf("Recover() legacy default journal: %v", err)
	}
	if commits != 0 {
		t.Fatalf("prepared legacy journal commit count = %d, want 0", commits)
	}
}

func TestAppendAndEnforceContextsCancelContendedLock(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.jsonl")
	layout := defaultLayout(path)
	lock, err := openLayoutLockFile(path, layout, true)
	if err != nil {
		t.Fatal(err)
	}
	unlock, err := acquireLayoutLock(context.Background(), lock, layout.LockTimeout)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := errors.Join(unlock(), lock.Close()); err != nil {
			t.Errorf("release held JSONL lock: %v", err)
		}
	})

	for _, test := range []struct {
		name string
		run  func(context.Context) error
	}{
		{name: "append", run: func(ctx context.Context) error {
			return AppendContext(ctx, path, []byte(`{"record":1}`), Policy{MaxBytes: 64, MaxArchives: 1})
		}},
		{name: "enforce", run: func(ctx context.Context) error {
			_, err := EnforceContext(ctx, path, Policy{MaxBytes: 64, MaxArchives: 1})
			return err
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				ctx, cancel := context.WithCancel(context.Background())
				result := make(chan error, 1)
				go func() { result <- test.run(ctx) }()
				synctest.Wait()
				cancel()
				if err := <-result; !errors.Is(err, context.Canceled) {
					t.Fatalf("canceled %s error = %v, want %v", test.name, err, context.Canceled)
				}
			})
		})
	}
}

func TestArchiveDirectoryRetryHonorsChurnBudgetAndCancellation(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		persistentChurn := func(string, int) ([]os.DirEntry, error) {
			return nil, fmt.Errorf("archive churn: %w", boundedio.ErrDirectorySnapshotChanged)
		}
		calls := 0
		_, err := readArchiveDirectoryContextWith(context.Background(), t.TempDir(), func(path string, maximum int) ([]os.DirEntry, error) {
			calls++
			return persistentChurn(path, maximum)
		})
		if !errors.Is(err, boundedio.ErrDirectorySnapshotChanged) || calls != archiveDirectoryReadTries {
			t.Fatalf("persistent churn = calls %d error %v, want %d attempts", calls, err, archiveDirectoryReadTries)
		}

		ctx, cancel := context.WithCancel(context.Background())
		calls = 0
		_, err = readArchiveDirectoryContextWith(ctx, t.TempDir(), func(path string, maximum int) ([]os.DirEntry, error) {
			calls++
			cancel()
			return persistentChurn(path, maximum)
		})
		if !errors.Is(err, context.Canceled) || calls != 1 {
			t.Fatalf("canceled churn = calls %d error %v, want one read and cancellation", calls, err)
		}

		deadlineContext, deadlineCancel := context.WithTimeout(context.Background(), 12*time.Millisecond)
		defer deadlineCancel()
		calls = 0
		_, err = readArchiveDirectoryContextWith(deadlineContext, t.TempDir(), func(path string, maximum int) ([]os.DirEntry, error) {
			calls++
			return persistentChurn(path, maximum)
		})
		if !errors.Is(err, context.DeadlineExceeded) || calls >= archiveDirectoryReadTries {
			t.Fatalf("deadline churn = calls %d error %v, want early deadline", calls, err)
		}
	})
}
