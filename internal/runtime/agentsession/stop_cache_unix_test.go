//go:build !windows

package agentsession

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
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
