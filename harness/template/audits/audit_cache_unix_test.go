//go:build !windows

package main

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestAuditCacheInputRejectsFIFOWithoutBlocking(t *testing.T) {
	path := filepath.Join(t.TempDir(), "input.fifo")
	if err := syscall.Mkfifo(path, 0o600); err != nil {
		t.Skipf("FIFO creation unavailable: %v", err)
	}
	inputs := newCacheInputs()
	inputs.AddFile(path)

	result := make(chan error, 1)
	go func() {
		_, err := inputs.Hash()
		result <- err
	}()
	select {
	case err := <-result:
		if err == nil || !strings.Contains(err.Error(), "non-symlink regular file") {
			t.Fatalf("FIFO cache input error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("FIFO cache input blocked")
	}
}

func TestAuditCacheStateRejectsFIFOWithoutBlocking(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit-results.json")
	if err := syscall.Mkfifo(path, 0o600); err != nil {
		t.Skipf("FIFO creation unavailable: %v", err)
	}
	result := make(chan cacheFile, 1)
	go func() { result <- loadCacheFile(path) }()
	select {
	case cache := <-result:
		if len(cache.Entries) != 0 {
			t.Fatalf("FIFO produced cache entries: %+v", cache)
		}
	case <-time.After(time.Second):
		t.Fatal("FIFO cache state blocked")
	}
}

func TestAuditCacheRejectsSymlinkedStateDirectory(t *testing.T) {
	root := t.TempDir()
	target := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".reconc"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(root, ".reconc", "cache")); err != nil {
		t.Fatal(err)
	}
	inputs := newCacheInputs()
	inputs.AddValue("fixture", "stable")
	runs := 0
	if result := runWithCache(root, "symlink-state", inputs, func() []string {
		runs++
		return nil
	}); len(result) != 0 {
		t.Fatalf("uncached audit result = %v", result)
	}
	if runs != 1 {
		t.Fatalf("audit runs = %d, want one uncached run", runs)
	}
	entries, err := os.ReadDir(target)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("symlink target received cache writes: %v", entries)
	}
}
