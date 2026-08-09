//go:build !windows

package policyproof_test

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"reconc.dev/reconc/internal/policyproof"
)

func TestLoadLatestRejectsFIFOWithoutBlocking(t *testing.T) {
	t.Setenv("RECONC_HOME", t.TempDir())
	repo := t.TempDir()
	path := policyproof.Path(repo)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(path, 0o600); err != nil {
		t.Skipf("FIFO creation unavailable: %v", err)
	}
	result := make(chan error, 1)
	go func() {
		_, _, err := policyproof.LoadLatest(repo)
		result <- err
	}()
	select {
	case err := <-result:
		if err == nil {
			t.Fatal("FIFO policy proof was accepted")
		}
	case <-time.After(time.Second):
		t.Fatal("FIFO policy proof blocked")
	}
}
