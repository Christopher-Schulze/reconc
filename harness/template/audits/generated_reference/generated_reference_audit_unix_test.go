//go:build !windows

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestAuditGeneratedReferenceDriftTimesOut(t *testing.T) {
	bin := t.TempDir()
	goStub := filepath.Join(bin, "go")
	if err := os.WriteFile(goStub, []byte("#!/bin/sh\nwhile :; do :; done\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	err := auditGeneratedReferenceDriftWithTimeout(t.TempDir(), "scripts/generators/generated_reference", 25*time.Millisecond)
	if err == nil || !strings.Contains(err.Error(), "timed out after 25ms") {
		t.Fatalf("expected deterministic timeout, got %v", err)
	}
}
