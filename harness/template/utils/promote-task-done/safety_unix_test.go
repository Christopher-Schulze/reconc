//go:build !windows

package main

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func TestReadRegularFileRejectsFIFOWithoutOpeningIt(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tasks.md")
	if err := syscall.Mkfifo(path, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readRegularFile(path); err == nil || !strings.Contains(err.Error(), "non-symlink regular file") {
		t.Fatalf("FIFO was not rejected before open: %v", err)
	}
}

func TestWriteAtomicRejectsSymlinkWithoutChangingTarget(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "target.md")
	link := filepath.Join(root, "tasks.md")
	if err := os.WriteFile(target, []byte("preserve"), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if err := writeAtomic(root, link, []byte("replacement"), []byte("preserve")); err == nil {
		t.Fatal("writeAtomic followed a symlink")
	}
	body, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "preserve" {
		t.Fatalf("symlink target changed to %q", body)
	}
}
