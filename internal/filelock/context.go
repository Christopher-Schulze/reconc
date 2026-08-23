package filelock

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"
)

var (
	// ErrLockTimeout identifies bounded contention that reached its deadline.
	ErrLockTimeout = errors.New("file lock acquisition timed out")
	// ErrLockCanceled identifies acquisition interrupted by its context.
	ErrLockCanceled = errors.New("file lock acquisition canceled")
)

// DefaultTimeout is the finite fallback used by production APIs that predate
// an explicit context parameter. Context-aware callers should pass their own
// operation budget instead.
const DefaultTimeout = 10 * time.Second

// AcquireError preserves an actionable acquisition class for callers while
// retaining the originating context error through errors.Is.
type AcquireError struct {
	Kind    error
	Timeout time.Duration
}

func (err *AcquireError) Error() string {
	if err == nil {
		return "file lock acquisition failed"
	}
	if err.Kind == ErrLockTimeout {
		return fmt.Sprintf("file lock acquisition timed out after %s", err.Timeout)
	}
	return err.Kind.Error()
}

func (err *AcquireError) Unwrap() error { return err.Kind }

// LockContext acquires an exclusive lock without an unbounded OS wait. It
// returns ErrLockCanceled when ctx is canceled and ErrLockTimeout when the
// finite timeout expires.
func LockContext(ctx context.Context, file *os.File, timeout time.Duration) (func() error, error) {
	return acquireContext(ctx, file, timeout, false)
}

// RLockContext is the shared-lock counterpart of LockContext.
func RLockContext(ctx context.Context, file *os.File, timeout time.Duration) (func() error, error) {
	return acquireContext(ctx, file, timeout, true)
}

func acquireContext(ctx context.Context, file *os.File, timeout time.Duration, shared bool) (func() error, error) {
	if ctx == nil {
		return nil, errors.New("file lock context is required")
	}
	if file == nil {
		return nil, errors.New("file lock descriptor is required")
	}
	if timeout <= 0 {
		return nil, errors.New("file lock timeout must be positive")
	}
	if err := ctx.Err(); err != nil {
		return nil, &AcquireError{Kind: errors.Join(ErrLockCanceled, err), Timeout: timeout}
	}
	boundedContext, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	for {
		unlock, err := tryLockContext(boundedContext, file, shared)
		if err == nil {
			return unlock, nil
		}
		if err := ctx.Err(); err != nil {
			return nil, &AcquireError{Kind: errors.Join(ErrLockCanceled, err), Timeout: timeout}
		}
		if errors.Is(boundedContext.Err(), context.DeadlineExceeded) {
			return nil, &AcquireError{Kind: ErrLockTimeout, Timeout: timeout}
		}
		if !IsContended(err) {
			return nil, err
		}
		select {
		case <-ctx.Done():
			return nil, &AcquireError{Kind: errors.Join(ErrLockCanceled, ctx.Err()), Timeout: timeout}
		case <-boundedContext.Done():
			if err := ctx.Err(); err != nil {
				return nil, &AcquireError{Kind: errors.Join(ErrLockCanceled, err), Timeout: timeout}
			}
			return nil, &AcquireError{Kind: ErrLockTimeout, Timeout: timeout}
		case <-ticker.C:
		}
	}
}
