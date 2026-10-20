package jsonl

import (
	"context"
	"errors"
)

func withLayoutLock(path string, layout Layout, fn func() error) error {
	return withLayoutLockContext(context.Background(), path, layout, fn)
}

func withLayoutLockContext(ctx context.Context, path string, layout Layout, fn func() error) error {
	return withLayoutLockModeContext(ctx, path, layout, true, fn)
}

func withLayoutLockModeContext(
	ctx context.Context,
	path string,
	layout Layout,
	create bool,
	fn func() error,
) error {
	if fn == nil {
		return errors.New("jsonl lock callback is required")
	}
	return withLayoutLockLeaseContext(ctx, path, layout, create, func(Layout) error {
		return fn()
	})
}
