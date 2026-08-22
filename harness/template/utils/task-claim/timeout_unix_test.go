//go:build !windows

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestAssertClaimsTimesOut(t *testing.T) {
	root := t.TempDir()
	binPath := filepath.Join(root, filepath.FromSlash(reconcBinaryRel()))
	if err := os.MkdirAll(filepath.Dir(binPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(binPath, []byte("#!/bin/sh\nwhile :; do :; done\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	bindClaimStubProvenance(t, root, binPath)
	err := assertClaimsWithTimeout(root, "TASK-0001-X", []string{"alpha"}, 25*time.Millisecond)
	if err == nil || !strings.Contains(err.Error(), "timed out after 25ms") {
		t.Fatalf("expected deterministic claim timeout, got %v", err)
	}
}
