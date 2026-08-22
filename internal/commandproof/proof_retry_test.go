package commandproof

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestGitIndexLockContentionRequiresTypedFailureAndRealLock(t *testing.T) {
	lockPath := filepath.Join(t.TempDir(), "index.lock")
	if err := os.WriteFile(lockPath, []byte("busy"), 0o600); err != nil {
		t.Fatal(err)
	}
	typed := &gitCommandError{args: []string{"write-tree"}, cause: errors.New("exit status 128")}
	if !gitIndexLockContention(typed, lockPath, false) {
		t.Fatal("typed Git failure plus regular index lock was not recognized")
	}
	if gitIndexLockContention(errors.New("fatal: index.lock"), lockPath, true) {
		t.Fatal("error text alone was treated as index-lock contention")
	}
	if err := os.Remove(lockPath); err != nil {
		t.Fatal(err)
	}
	if gitIndexLockContention(typed, lockPath, false) {
		t.Fatal("typed Git failure without an index lock was treated as contention")
	}
	if !gitIndexLockContention(typed, lockPath, true) {
		t.Fatal("typed Git failure lost a lock observed before command execution")
	}
}

func TestGitWriteTreeUsesOneBoundedIndexLockDeadline(t *testing.T) {
	repo := newProofRepo(t)
	lockPath := filepath.Join(repo, ".git", "index.lock")
	if err := os.WriteFile(lockPath, []byte("busy"), 0o600); err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	_, err := gitWriteTreeWithTimeout(repo, 150*time.Millisecond)
	if err == nil || !strings.Contains(err.Error(), "remained locked") {
		t.Fatalf("gitWriteTreeWithTimeout() error = %v, want bounded lock contention", err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("lock retry exceeded its total deadline: %s", elapsed)
	}
}
