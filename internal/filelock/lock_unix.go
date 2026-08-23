//go:build !windows

package filelock

import (
	"context"
	"errors"
	"os"
	"syscall"
)

// Lock takes a blocking exclusive lock on file and returns its unlock
// function. The caller must keep file open until after unlock.
func Lock(file *os.File) (func() error, error) {
	if err := flockWithRetry(context.Background(), syscall.Flock, int(file.Fd()), syscall.LOCK_EX); err != nil {
		return nil, err
	}
	return func() error {
		return flockWithRetry(context.Background(), syscall.Flock, int(file.Fd()), syscall.LOCK_UN)
	}, nil
}

// TryLock takes an exclusive lock without waiting for another owner.
func TryLock(file *os.File) (func() error, error) {
	return tryLockContext(context.Background(), file, false)
}

// RLock takes a blocking shared lock on file and returns its unlock function.
func RLock(file *os.File) (func() error, error) {
	if err := flockWithRetry(context.Background(), syscall.Flock, int(file.Fd()), syscall.LOCK_SH); err != nil {
		return nil, err
	}
	return func() error {
		return flockWithRetry(context.Background(), syscall.Flock, int(file.Fd()), syscall.LOCK_UN)
	}, nil
}

// TryRLock takes a shared lock without waiting for an exclusive owner.
func TryRLock(file *os.File) (func() error, error) {
	return tryLockContext(context.Background(), file, true)
}

func tryLockContext(ctx context.Context, file *os.File, shared bool) (func() error, error) {
	operation := syscall.LOCK_EX | syscall.LOCK_NB
	if shared {
		operation = syscall.LOCK_SH | syscall.LOCK_NB
	}
	if err := flockWithRetry(ctx, syscall.Flock, int(file.Fd()), operation); err != nil {
		return nil, err
	}
	return func() error {
		return flockWithRetry(context.Background(), syscall.Flock, int(file.Fd()), syscall.LOCK_UN)
	}, nil
}

func flockWithRetry(ctx context.Context, call func(int, int) error, descriptor, operation int) error {
	for {
		err := call(descriptor, operation)
		if !errors.Is(err, syscall.EINTR) {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
	}
}

// IsContended reports whether a non-blocking lock failed only because another
// owner currently holds an incompatible lock.
func IsContended(err error) bool {
	return errors.Is(err, syscall.EAGAIN) || errors.Is(err, syscall.EWOULDBLOCK)
}
