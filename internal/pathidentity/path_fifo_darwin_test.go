//go:build darwin

package pathidentity

import (
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestResolveExistingDoesNotBlockOnAFIFO(t *testing.T) {
	fifo := filepath.Join(t.TempDir(), "pipe")
	if err := unix.Mkfifo(fifo, 0o600); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := ResolveExisting(fifo)
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("resolve FIFO: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("ResolveExisting blocked on a FIFO")
	}
}
