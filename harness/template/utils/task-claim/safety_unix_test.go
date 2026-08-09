//go:build !windows

package main

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func TestResolveTaskRejectsFIFOWithoutOpeningIt(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "docs"), 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, filepath.FromSlash(tasksRel))
	if err := syscall.Mkfifo(path, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := resolveTask(root, ""); err == nil || !strings.Contains(err.Error(), "non-symlink regular file") {
		t.Fatalf("FIFO was not rejected before open: %v", err)
	}
}

func TestAssertClaimsRejectsSymlinkedBinary(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "target")
	if err := os.WriteFile(target, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, filepath.FromSlash(reconcBinaryRel()))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, path); err != nil {
		t.Fatal(err)
	}
	if err := assertClaims(root, "TASK-0001-X", []string{"ci-green"}); err == nil || !strings.Contains(err.Error(), "non-symlink executable") {
		t.Fatalf("symlinked binary was accepted: %v", err)
	}
}
