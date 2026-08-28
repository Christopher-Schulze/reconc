//go:build !windows

package atomicfile

import (
	"errors"
	"testing"
)

type faultingDirectorySyncCloser struct {
	syncErr  error
	closeErr error
	synced   bool
	closed   bool
}

func (directory *faultingDirectorySyncCloser) Sync() error {
	directory.synced = true
	return directory.syncErr
}

func (directory *faultingDirectorySyncCloser) Close() error {
	directory.closed = true
	return directory.closeErr
}

func TestSyncAndCloseDirectoryPreservesEveryFailure(t *testing.T) {
	syncErr := errors.New("sync failed")
	closeErr := errors.New("close failed")
	for _, test := range []struct {
		name     string
		syncErr  error
		closeErr error
	}{
		{name: "success"},
		{name: "sync", syncErr: syncErr},
		{name: "close", closeErr: closeErr},
		{name: "both", syncErr: syncErr, closeErr: closeErr},
	} {
		t.Run(test.name, func(t *testing.T) {
			directory := &faultingDirectorySyncCloser{syncErr: test.syncErr, closeErr: test.closeErr}
			err := syncAndCloseDirectory(directory)
			if !directory.synced || !directory.closed ||
				test.syncErr != nil && !errors.Is(err, test.syncErr) ||
				test.closeErr != nil && !errors.Is(err, test.closeErr) ||
				test.syncErr == nil && test.closeErr == nil && err != nil {
				t.Fatalf("result = %v, synced=%v closed=%v", err, directory.synced, directory.closed)
			}
		})
	}
}
