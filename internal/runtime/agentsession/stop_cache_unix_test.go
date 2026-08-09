//go:build !windows

package agentsession

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// TestWorktreePathHashSeparatesSymlinksAndSpecialFiles keeps the identity
// primitive honest about shapes a repository can legitimately contain. A
// symlink must be identified by its target rather than followed, and a special
// file must never be read as content.
func TestWorktreePathHashSeparatesSymlinksAndSpecialFiles(t *testing.T) {
	repo := t.TempDir()
	if err := os.WriteFile(filepath.Join(repo, "target.txt"), []byte("body\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("target.txt", filepath.Join(repo, "link.txt")); err != nil {
		t.Fatal(err)
	}
	direct := worktreePathHash(repo, "target.txt", "")
	link := worktreePathHash(repo, "link.txt", "")
	if link == direct {
		t.Fatal("a symlink must not share the identity of the file it points at")
	}
	if err := os.Remove(filepath.Join(repo, "link.txt")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("other.txt", filepath.Join(repo, "link.txt")); err != nil {
		t.Fatal(err)
	}
	if repointed := worktreePathHash(repo, "link.txt", ""); repointed == link {
		t.Fatal("repointing a symlink must change its identity")
	}

	fifo := filepath.Join(repo, "pipe")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Skipf("FIFO creation unsupported on this host: %v", err)
	}
	identity := worktreePathHash(repo, "pipe", "")
	if identity == direct || identity == "missing" {
		t.Fatalf("a special file must carry its own identity, got %q", identity)
	}
}

func TestPolicyInputTrustFollowsContainedSymlinksAndRejectsUnsafeShapes(t *testing.T) {
	repo := t.TempDir()
	target := filepath.Join(repo, "target.txt")
	if err := os.WriteFile(target, []byte("one\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("target.txt", filepath.Join(repo, "link.txt")); err != nil {
		t.Fatal(err)
	}
	first := stopPolicyInputIdentity(repo, "link.txt")
	if !first.Trusted {
		t.Fatalf("contained symlink target was not bound exactly: %+v", first)
	}
	if err := os.WriteFile(target, []byte("two\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	second := stopPolicyInputIdentity(repo, "link.txt")
	if !second.Trusted || second.Identity == first.Identity {
		t.Fatalf("symlink target content change stayed invisible: first=%+v second=%+v", first, second)
	}

	outside := filepath.Join(t.TempDir(), "outside.txt")
	if err := os.WriteFile(outside, []byte("outside\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(repo, "link.txt")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(repo, "link.txt")); err != nil {
		t.Fatal(err)
	}
	escaping := stopPolicyInputIdentity(repo, "link.txt")
	if escaping.Trusted || !strings.HasPrefix(escaping.Identity, "resolve-error:") {
		t.Fatalf("escaping symlink was cache-trusted: %+v", escaping)
	}

	directory := filepath.Join(repo, "tree")
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("../target.txt", filepath.Join(directory, "nested-link")); err != nil {
		t.Fatal(err)
	}
	nested := stopPolicyInputIdentity(repo, "tree")
	if nested.Trusted || !strings.Contains(nested.Identity, "tree contains symlink") {
		t.Fatalf("directory containing a symlink was cache-trusted: %+v", nested)
	}

	fifo := filepath.Join(repo, "pipe")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Skipf("FIFO creation unsupported on this host: %v", err)
	}
	special := stopPolicyInputIdentity(repo, "pipe")
	if special.Trusted || !strings.HasPrefix(special.Identity, "mode:") {
		t.Fatalf("special policy input was cache-trusted: %+v", special)
	}
	if err := os.Symlink("pipe", filepath.Join(repo, "pipe-link")); err != nil {
		t.Fatal(err)
	}
	linkedSpecial := stopPolicyInputIdentity(repo, "pipe-link")
	if linkedSpecial.Trusted || !strings.HasPrefix(linkedSpecial.Identity, "mode:") {
		t.Fatalf("symlinked special policy input was cache-trusted: %+v", linkedSpecial)
	}
}

func TestFreshFileExpiryUsesContainedSymlinkTarget(t *testing.T) {
	repo := t.TempDir()
	target := filepath.Join(repo, "target.txt")
	if err := os.WriteFile(target, []byte("current\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	modified := time.Now().Add(-30 * time.Minute)
	if err := os.Chtimes(target, modified, modified); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("target.txt", filepath.Join(repo, "STATUS.md")); err != nil {
		t.Fatal(err)
	}
	want := modified.Add(2 * time.Hour).Unix()
	got := stopPolicyReportExpiry(repo, []stopPolicyFreshFile{{Path: "STATUS.md", MaxAgeHours: 2}})
	if got != want {
		t.Fatalf("symlinked fresh-file expiry = %d, want target expiry %d", got, want)
	}
}

func TestPolicyInputIdentityBindsExecutableMode(t *testing.T) {
	repo := t.TempDir()
	path := filepath.Join(repo, "scripts", "check.sh")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	before := stopPolicyInputIdentity(repo, "scripts/check.sh")
	if err := os.Chmod(path, 0o755); err != nil {
		t.Fatal(err)
	}
	after := stopPolicyInputIdentity(repo, "scripts/check.sh")
	if !before.Trusted || !after.Trusted || before.Identity == after.Identity {
		t.Fatalf("execute-bit-only policy change stayed invisible: before=%+v after=%+v", before, after)
	}
}
