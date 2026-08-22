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
	if err := assertClaims(root, "TASK-0001-X", []string{"ci-green"}); err == nil || !strings.Contains(err.Error(), "non-symlink regular file") {
		t.Fatalf("symlinked binary was accepted: %v", err)
	}
}

func TestAssertClaimsRejectsNonExecutableBinary(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, filepath.FromSlash(reconcBinaryRel()))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	bindClaimStubProvenance(t, root, path)
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := assertClaims(root, "TASK-0001-X", []string{"ci-green"}); err == nil || !strings.Contains(err.Error(), "is not executable") {
		t.Fatalf("non-executable binary was accepted: %v", err)
	}
}

func TestAssertClaimsStopsAfterRepoLocalBinaryReplacement(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, filepath.FromSlash(reconcBinaryRel()))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	logPath := filepath.Join(root, "claims.log")
	replacement := filepath.Join(filepath.Dir(path), "replacement")
	if err := os.WriteFile(replacement, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeClaimStub(t, root, path, "", `#!/bin/sh
printf '%s\n' "$*" >> "$RECONC_STUB_LOG"
if [ "$4" = "alpha" ]; then
  mv "$RECONC_STUB_REPLACEMENT" "$RECONC_STUB_TARGET"
fi
exit 0
`)
	t.Setenv("RECONC_STUB_LOG", logPath)
	t.Setenv("RECONC_STUB_REPLACEMENT", replacement)
	t.Setenv("RECONC_STUB_TARGET", path)
	err := assertClaims(root, "TASK-0001-X", []string{"alpha", "beta"})
	if err == nil || !strings.Contains(err.Error(), "filesystem identity changed") {
		t.Fatalf("replaced binary retained claim authority: %v", err)
	}
	log, readErr := os.ReadFile(logPath)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if !strings.Contains(string(log), " alpha") || strings.Contains(string(log), " beta") {
		t.Fatalf("replacement claim log = %q, want alpha only", log)
	}
}
