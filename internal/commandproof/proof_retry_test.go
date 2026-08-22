package commandproof

import (
	"context"
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
	lockPath := filepath.Join(t.TempDir(), "index.lock")
	if err := os.WriteFile(lockPath, []byte("busy"), 0o600); err != nil {
		t.Fatal(err)
	}
	writeAttempts := 0
	runner := func(ctx context.Context, _ string, args ...string) (string, error) {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		if len(args) == 3 && args[0] == "rev-parse" && args[1] == "--git-path" && args[2] == "index.lock" {
			return lockPath, nil
		}
		if len(args) == 1 && args[0] == "write-tree" {
			writeAttempts++
			return "", &gitCommandError{args: []string{"write-tree"}, cause: errors.New("exit status 128")}
		}
		t.Fatalf("unexpected Git arguments: %v", args)
		return "", nil
	}
	started := time.Now()
	_, err := gitWriteTreeWithRunner("unused", 150*time.Millisecond, runner)
	if err == nil || !strings.Contains(err.Error(), "remained locked") {
		t.Fatalf("gitWriteTreeWithRunner() error = %v, want bounded lock contention", err)
	}
	if writeAttempts < 2 {
		t.Fatalf("write-tree attempts = %d, want at least two retries before the deadline", writeAttempts)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("lock retry exceeded its total deadline: %s", elapsed)
	}
}
