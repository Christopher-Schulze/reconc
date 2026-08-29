package tasklifecycle

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestMutationLockRejectsSymlinkedComponents(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation is not reliably available")
	}
	tests := []struct {
		name  string
		setup func(*testing.T, string)
	}{
		{
			name: "reconc parent",
			setup: func(t *testing.T, repo string) {
				target := t.TempDir()
				if err := os.Symlink(target, filepath.Join(repo, ".reconc")); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "lock parent",
			setup: func(t *testing.T, repo string) {
				if err := os.Mkdir(filepath.Join(repo, ".reconc"), 0o755); err != nil {
					t.Fatal(err)
				}
				target := t.TempDir()
				if err := os.Symlink(target, filepath.Join(repo, ".reconc", "locks")); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "lock file",
			setup: func(t *testing.T, repo string) {
				if err := os.MkdirAll(filepath.Join(repo, ".reconc", "locks"), 0o755); err != nil {
					t.Fatal(err)
				}
				target := filepath.Join(t.TempDir(), "target")
				if err := os.WriteFile(target, []byte("unchanged\n"), 0o600); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(target, filepath.Join(repo, filepath.FromSlash(taskLockRel))); err != nil {
					t.Fatal(err)
				}
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			repo := t.TempDir()
			test.setup(t, repo)
			if err := withMutationLock(repo, func() error { return nil }); err == nil {
				t.Fatal("symlinked TASK lock component was accepted")
			}
		})
	}
}

func TestMutationLockLeaseRejectsCrossProcessInodeSplitting(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("lock-path replacement is not deterministic with Windows sharing semantics")
	}
	for _, action := range []string{"replace", "unlink"} {
		t.Run(action, func(t *testing.T) {
			testMutationLockSplit(t, action)
		})
	}
}

func testMutationLockSplit(t *testing.T, action string) {
	t.Helper()
	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, "docs"), 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(repo, "docs", "tasks.md")
	if err := os.WriteFile(path, []byte("before\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	signalPath := filepath.Join(repo, "prepare.signal")
	releasePath := filepath.Join(repo, "prepare.release")
	resultPath := filepath.Join(repo, "helper.result")
	cmd := exec.Command(os.Args[0], "-test.run=^TestMutationLockLeaseHelper$")
	cmd.Env = append(os.Environ(),
		"TASK_LOCK_LEASE_HELPER=1",
		"TASK_LOCK_LEASE_REPO="+repo,
		"TASK_LOCK_LEASE_SIGNAL="+signalPath,
		"TASK_LOCK_LEASE_RELEASE="+releasePath,
		"TASK_LOCK_LEASE_RESULT="+resultPath,
	)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = os.WriteFile(releasePath, nil, 0o600)
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})
	waitForTaskLockFile(t, signalPath)
	lockPath := filepath.Join(repo, filepath.FromSlash(taskLockRel))
	if action == "replace" {
		if err := os.Rename(lockPath, lockPath+".moved"); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(lockPath, nil, 0o644); err != nil {
			t.Fatal(err)
		}
	} else if err := os.Remove(lockPath); err != nil {
		t.Fatal(err)
	}
	if err := withMutationLockLease(repo, func(lease *taskMutationLockLease) error {
		return applyTransactionWithLease(repo, "parent", []fileMutation{{Path: "docs/tasks.md", After: []byte("parent\n")}}, nil, lease)
	}); err != nil {
		t.Fatalf("replacement owner transaction: %v", err)
	}
	if err := os.WriteFile(releasePath, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Wait(); err != nil {
		result, _ := os.ReadFile(resultPath)
		t.Fatalf("stale lock owner failed: %v; result=%q", err, result)
	}
	result, err := os.ReadFile(resultPath)
	if err != nil || !strings.Contains(string(result), "lock lease changed") {
		t.Fatalf("stale lock owner result = %q; want lock lease rejection", result)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "parent\n" {
		t.Fatalf("TASK overview after replacement = %q, want %q", body, "parent\n")
	}
}

func TestMutationLockLeaseHelper(t *testing.T) {
	if os.Getenv("TASK_LOCK_LEASE_HELPER") != "1" {
		return
	}
	repo := os.Getenv("TASK_LOCK_LEASE_REPO")
	signalPath := os.Getenv("TASK_LOCK_LEASE_SIGNAL")
	releasePath := os.Getenv("TASK_LOCK_LEASE_RELEASE")
	resultPath := os.Getenv("TASK_LOCK_LEASE_RESULT")
	err := withMutationLockLease(repo, func(lease *taskMutationLockLease) error {
		if err := os.WriteFile(signalPath, nil, 0o600); err != nil {
			return err
		}
		deadline := time.Now().Add(5 * time.Second)
		for {
			if _, err := os.Lstat(releasePath); err == nil {
				return applyTransactionWithLease(repo, "child", []fileMutation{{Path: "docs/tasks.md", After: []byte("child\n")}}, nil, lease)
			} else if !errors.Is(err, os.ErrNotExist) {
				return err
			}
			if time.Now().After(deadline) {
				return errors.New("timed out waiting for TASK lock replacement release")
			}
			time.Sleep(5 * time.Millisecond)
		}
	})
	_ = os.WriteFile(resultPath, []byte(taskLockErrorString(err)), 0o600)
	if err == nil || !strings.Contains(err.Error(), "lock lease changed") {
		t.Fatalf("stale TASK lock owner error = %v, want lock lease rejection", err)
	}
}

func taskLockErrorString(err error) string {
	if err == nil {
		return "<nil>"
	}
	return err.Error()
}

func waitForTaskLockFile(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Lstat(path); err == nil {
			return
		} else if !errors.Is(err, os.ErrNotExist) {
			t.Fatal(err)
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", path)
		}
		time.Sleep(5 * time.Millisecond)
	}
}
