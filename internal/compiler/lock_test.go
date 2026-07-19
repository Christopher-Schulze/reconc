package compiler

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAcquireCompileLockCreatesFile(t *testing.T) {
	repo := t.TempDir()
	release, err := AcquireCompileLock(repo)
	if err != nil {
		t.Fatalf("AcquireCompileLock: %v", err)
	}
	defer func() {
		if err := release(); err != nil {
			t.Error(err)
		}
	}()

	if _, err := os.Stat(filepath.Join(repo, CompileLockRelativePath)); err != nil {
		t.Errorf("expected lock file to exist, got: %v", err)
	}
}

func TestAcquireCompileLockReleaseUnlocks(t *testing.T) {
	repo := t.TempDir()
	release, err := AcquireCompileLock(repo)
	if err != nil {
		t.Fatal(err)
	}
	if err := release(); err != nil {
		t.Fatal(err)
	}
	releaseAgain, err := AcquireCompileLock(repo)
	if err != nil {
		t.Fatalf("persistent unlocked file must be reusable: %v", err)
	}
	if err := releaseAgain(); err != nil {
		t.Fatal(err)
	}
}

func TestAcquireCompileLockSecondCallBlocks(t *testing.T) {
	repo := t.TempDir()
	release, err := AcquireCompileLock(repo)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := release(); err != nil {
			t.Error(err)
		}
	}()

	_, err = AcquireCompileLock(repo)
	if err == nil {
		t.Fatal("expected second lock acquisition to fail")
	}
	if !strings.Contains(err.Error(), "in progress") {
		t.Errorf("expected 'in progress' error, got: %v", err)
	}
}

func TestAcquireCompileLockReusesPersistentFile(t *testing.T) {
	repo := t.TempDir()
	lockPath := filepath.Join(repo, CompileLockRelativePath)
	if err := os.MkdirAll(filepath.Dir(lockPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(lockPath, []byte("pid=99999 acquired=old\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	release, err := AcquireCompileLock(repo)
	if err != nil {
		t.Fatalf("persistent unlocked file should be reusable, got: %v", err)
	}
	if err := release(); err != nil {
		t.Fatal(err)
	}
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
	defer func() {
		if err := release(); err != nil {
			t.Error(err)
		}
	}()

	_, err = CompileRepoPolicy(repo, "0.1.0-test")
	if err == nil {
		t.Fatal("expected CompileRepoPolicy to fail while advisory lock is held")
	}
	if !strings.Contains(err.Error(), "in progress") {
		t.Fatalf("expected lock contention error, got: %v", err)
	}
}
