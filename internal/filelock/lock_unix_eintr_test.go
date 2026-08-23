//go:build !windows

package filelock

import (
	"context"
	"errors"
	"syscall"
	"testing"
	"time"
)

func TestFlockWithRetryPreservesOperationAndRetriesEINTR(t *testing.T) {
	calls := 0
	err := flockWithRetry(context.Background(), func(descriptor, operation int) error {
		calls++
		if descriptor != 42 || operation != syscall.LOCK_EX|syscall.LOCK_NB {
			t.Fatalf("flock call = (%d, %d)", descriptor, operation)
		}
		if calls < 4 {
			return syscall.EINTR
		}
		return nil
	}, 42, syscall.LOCK_EX|syscall.LOCK_NB)
	if err != nil || calls != 4 {
		t.Fatalf("retry result = %v after %d calls", err, calls)
	}
}

func TestFlockWithRetryPreservesContentionAfterInterruption(t *testing.T) {
	calls := 0
	err := flockWithRetry(context.Background(), func(int, int) error {
		calls++
		if calls == 1 {
			return syscall.EINTR
		}
		return syscall.EWOULDBLOCK
	}, 42, syscall.LOCK_SH|syscall.LOCK_NB)
	if !errors.Is(err, syscall.EWOULDBLOCK) || !IsContended(err) || calls != 2 {
		t.Fatalf("contention result = %v after %d calls", err, calls)
	}
}

func TestFlockWithRetryStopsRepeatedEINTRAtContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	calls := 0
	err := flockWithRetry(ctx, func(int, int) error {
		calls++
		if calls == 3 {
			cancel()
		}
		return syscall.EINTR
	}, 42, syscall.LOCK_UN)
	if !errors.Is(err, context.Canceled) || calls != 3 {
		t.Fatalf("cancellation result = %v after %d calls", err, calls)
	}
}

func TestFlockWithRetryStopsRepeatedEINTRAtContextDeadline(t *testing.T) {
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	calls := 0
	err := flockWithRetry(ctx, func(int, int) error {
		calls++
		return syscall.EINTR
	}, 42, syscall.LOCK_EX)
	if !errors.Is(err, context.DeadlineExceeded) || calls != 1 {
		t.Fatalf("deadline result = %v after %d calls", err, calls)
	}
}
