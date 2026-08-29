package jsonl

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestLockLeaseRejectsCrossProcessInodeSplitting(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("lock-path replacement is not deterministic with Windows sharing semantics")
	}
	for _, action := range []string{"replace", "unlink"} {
		t.Run(action, func(t *testing.T) {
			testLockLeaseSplit(t, action)
		})
	}
}

func testLockLeaseSplit(t *testing.T, action string) {
	t.Helper()
	directory := t.TempDir()
	path := filepath.Join(directory, "events.jsonl")
	signalPath := filepath.Join(directory, "prepare.signal")
	releasePath := filepath.Join(directory, "prepare.release")
	resultPath := filepath.Join(directory, "helper.result")
	cmd := exec.Command(os.Args[0], "-test.run=^TestLockLeaseReplacementHelper$")
	cmd.Env = append(os.Environ(),
		"JSONL_LOCK_LEASE_HELPER=1",
		"JSONL_LOCK_LEASE_PATH="+path,
		"JSONL_LOCK_LEASE_SIGNAL="+signalPath,
		"JSONL_LOCK_LEASE_RELEASE="+releasePath,
		"JSONL_LOCK_LEASE_RESULT="+resultPath,
	)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = os.WriteFile(releasePath, nil, 0o600)
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})
	waitForLockLeaseFile(t, signalPath)
	layout := defaultLayout(path)
	if action == "replace" {
		if err := os.Rename(layout.LockPath, layout.LockPath+".moved"); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(layout.LockPath, nil, 0o600); err != nil {
			t.Fatal(err)
		}
	} else if err := os.Remove(layout.LockPath); err != nil {
		t.Fatal(err)
	}
	if err := Append(path, []byte("parent"), Policy{MaxBytes: 64, MaxArchives: 1}); err != nil {
		t.Fatalf("replacement owner append: %v", err)
	}
	if err := os.WriteFile(releasePath, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Wait(); err != nil {
		result, _ := os.ReadFile(resultPath)
		t.Fatalf("lock-lease helper failed: %v; result=%q", err, result)
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
		t.Fatalf("live JSONL after replacement = %q, want %q", body, "parent\n")
	}
}

func TestLockLeaseReplacementHelper(t *testing.T) {
	if os.Getenv("JSONL_LOCK_LEASE_HELPER") != "1" {
		return
	}
	path := os.Getenv("JSONL_LOCK_LEASE_PATH")
	signalPath := os.Getenv("JSONL_LOCK_LEASE_SIGNAL")
	releasePath := os.Getenv("JSONL_LOCK_LEASE_RELEASE")
	resultPath := os.Getenv("JSONL_LOCK_LEASE_RESULT")
	err := AppendTransactionContextWithLayout(
		context.Background(), path, Policy{MaxBytes: 64, MaxArchives: 1}, defaultLayout(path),
		func() ([]byte, error) {
			if err := os.WriteFile(signalPath, nil, 0o600); err != nil {
				return nil, err
			}
			deadline := time.Now().Add(5 * time.Second)
			for {
				if _, err := os.Lstat(releasePath); err == nil {
					return []byte("child"), nil
				} else if !errors.Is(err, os.ErrNotExist) {
					return nil, err
				}
				if time.Now().After(deadline) {
					return nil, errors.New("timed out waiting for replacement test release")
				}
				time.Sleep(5 * time.Millisecond)
			}
		},
		nil,
	)
	_ = os.WriteFile(resultPath, []byte(errorString(err)), 0o600)
	if err == nil || !strings.Contains(err.Error(), "lock lease changed") {
		t.Fatalf("stale lock owner error = %v, want lock lease rejection", err)
	}
}

func errorString(err error) string {
	if err == nil {
		return "<nil>"
	}
	return err.Error()
}

func waitForLockLeaseFile(t *testing.T, path string) {
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
