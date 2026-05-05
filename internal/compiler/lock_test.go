package compiler

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestAcquireCompileLockCreatesFile(t *testing.T) {
	repo := t.TempDir()
	release, err := AcquireCompileLock(repo)
	if err != nil {
		t.Fatalf("AcquireCompileLock: %v", err)
	}
	defer release()

	if _, err := os.Stat(filepath.Join(repo, CompileLockRelativePath)); err != nil {
		t.Errorf("expected lock file to exist, got: %v", err)
	}
}

func TestAcquireCompileLockReleaseRemoves(t *testing.T) {
	repo := t.TempDir()
	release, err := AcquireCompileLock(repo)
	if err != nil {
		t.Fatal(err)
	}
	release()
	if _, err := os.Stat(filepath.Join(repo, CompileLockRelativePath)); !os.IsNotExist(err) {
		t.Errorf("expected lock file removed after release; stat err: %v", err)
	}
}

func TestAcquireCompileLockSecondCallBlocks(t *testing.T) {
	repo := t.TempDir()
	release, err := AcquireCompileLock(repo)
	if err != nil {
		t.Fatal(err)
	}
	defer release()

	_, err = AcquireCompileLock(repo)
	if err == nil {
		t.Fatal("expected second lock acquisition to fail")
	}
	if !strings.Contains(err.Error(), "in progress") {
		t.Errorf("expected 'in progress' error, got: %v", err)
	}
}

func TestAcquireCompileLockReapsStale(t *testing.T) {
	repo := t.TempDir()
	// Create a lock file and backdate its mtime so the code sees it
	// as stale (older than StaleCompileLockAfter).
	lockPath := filepath.Join(repo, CompileLockRelativePath)
	if err := os.MkdirAll(filepath.Dir(lockPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(lockPath, []byte("pid=99999 acquired=old\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	stale := time.Now().Add(-2 * StaleCompileLockAfter)
	if err := os.Chtimes(lockPath, stale, stale); err != nil {
		t.Fatal(err)
	}

	release, err := AcquireCompileLock(repo)
	if err != nil {
		t.Fatalf("stale lock should be reaped, got: %v", err)
	}
	defer release()
}

func TestCompileRepoPolicyRespectsExistingLock(t *testing.T) {
	// Deterministic integration test: when another compile already
	// holds the advisory lock, CompileRepoPolicy must fail closed
	// instead of racing on the lockfile.
	t.Setenv("RECONC_HOME", t.TempDir())
	repo := t.TempDir()
	writeFile(t, repo, "AGENTS.md", "# t\n")

	release, err := AcquireCompileLock(repo)
	if err != nil {
		t.Fatalf("AcquireCompileLock: %v", err)
	}
	defer release()

	_, err = CompileRepoPolicy(repo, "0.1.0-test")
	if err == nil {
		t.Fatal("expected CompileRepoPolicy to fail while advisory lock is held")
	}
	if !strings.Contains(err.Error(), "in progress") {
		t.Fatalf("expected lock contention error, got: %v", err)
	}
}
