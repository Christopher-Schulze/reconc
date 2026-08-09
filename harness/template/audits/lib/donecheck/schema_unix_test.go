//go:build !windows

package donecheck

import (
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestLoadSchemaRejectsFIFOWithoutBlocking(t *testing.T) {
	path := filepath.Join(t.TempDir(), "task-schema.yaml")
	if err := syscall.Mkfifo(path, 0o600); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := LoadSchema(path)
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("LoadSchema accepted FIFO")
		}
	case <-time.After(time.Second):
		t.Fatal("LoadSchema blocked on FIFO")
	}
}
