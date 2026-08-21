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

func TestAcquireCompileLockRejectsUnsafeParentAndLockObjects(t *testing.T) {
	tests := []struct {
		name  string
		setup func(*testing.T, string)
	}{
		{name: "parent file", setup: func(t *testing.T, repo string) {
			if err := os.WriteFile(filepath.Join(repo, ".reconc"), []byte("not a directory"), 0o600); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "lock directory", setup: func(t *testing.T, repo string) {
			if err := os.MkdirAll(filepath.Join(repo, CompileLockRelativePath), 0o700); err != nil {
				t.Fatal(err)
			}
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			repo := t.TempDir()
			test.setup(t, repo)
			if _, err := AcquireCompileLock(repo); err == nil {
				t.Fatal("unsafe compile lock object was accepted")
			}
		})
	}
}

func TestAcquireCompileLockRejectsSymlinksWithoutChangingTarget(t *testing.T) {
	repo := t.TempDir()
	target := filepath.Join(t.TempDir(), "target")
	want := []byte("unchanged\n")
	if err := os.WriteFile(target, want, 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(repo, ".reconc"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(repo, CompileLockRelativePath)); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if _, err := AcquireCompileLock(repo); err == nil {
		t.Fatal("symlinked compile lock was accepted")
	}
	got, err := os.ReadFile(target)
	if err != nil || string(got) != string(want) {
		t.Fatalf("symlink target changed: body=%q err=%v", got, err)
	}
	assertRepresentableFileMode(t, target, 0o640)
}

func TestAcquireCompileLockRejectsSymlinkedParentWithoutCreatingTarget(t *testing.T) {
	repo := t.TempDir()
	target := t.TempDir()
	if err := os.Symlink(target, filepath.Join(repo, ".reconc")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if _, err := AcquireCompileLock(repo); err == nil {
		t.Fatal("symlinked compile lock parent was accepted")
	}
	if _, err := os.Lstat(filepath.Join(target, ".compile.lock")); !os.IsNotExist(err) {
		t.Fatalf("compile lock was created through symlinked parent: %v", err)
	}
}

func TestOpenCompileLockFileRejectsIdentitySwapBeforeLocking(t *testing.T) {
	repo := t.TempDir()
	directory, err := os.OpenRoot(repo)
	if err != nil {
		t.Fatal(err)
	}
	defer directory.Close()
	name := ".compile.lock"
	if err := os.WriteFile(filepath.Join(repo, name), []byte("original"), 0o600); err != nil {
		t.Fatal(err)
	}
	before, err := directory.Lstat(name)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(repo, name), filepath.Join(repo, name+".old")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, name), []byte("replacement"), 0o600); err != nil {
		t.Fatal(err)
	}
	if file, err := openCompileLockFile(directory, name, before); err == nil {
		_ = file.Close()
		t.Fatal("compile lock identity swap was accepted")
	}
}
