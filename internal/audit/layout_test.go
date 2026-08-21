package audit

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"reconc.dev/reconc/internal/jsonl"
)

func TestAuditAppendGateSerializesAndCleansUp(t *testing.T) {
	repo := t.TempDir()
	releaseFirst, err := acquireAuditAppendGate(context.Background(), repo, time.Second)
	if err != nil {
		t.Fatal(err)
	}

	started := make(chan struct{})
	acquired := make(chan func(), 1)
	errs := make(chan error, 1)
	go func() {
		close(started)
		release, err := acquireAuditAppendGate(context.Background(), repo, time.Second)
		if err != nil {
			errs <- err
			return
		}
		acquired <- release
	}()
	<-started
	select {
	case release := <-acquired:
		release()
		t.Fatal("second audit append gate acquisition did not wait")
	case err := <-errs:
		t.Fatalf("second audit append gate acquisition failed: %v", err)
	case <-time.After(20 * time.Millisecond):
	}

	releaseFirst()
	select {
	case release := <-acquired:
		release()
	case err := <-errs:
		t.Fatalf("second audit append gate acquisition failed: %v", err)
	case <-time.After(time.Second):
		t.Fatal("second audit append gate acquisition did not recover")
	}
	assertAuditAppendGatesEmpty(t)
}

func TestAuditAppendGateTimeoutCancellationAndRecovery(t *testing.T) {
	repo := t.TempDir()
	release, err := acquireAuditAppendGate(context.Background(), repo, time.Second)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := acquireAuditAppendGate(context.Background(), repo, 10*time.Millisecond); !errors.Is(err, errAuditAppendGateTimeout) {
		t.Fatalf("timed acquisition error = %v, want %v", err, errAuditAppendGateTimeout)
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancelStarted := make(chan struct{})
	cancelResult := make(chan error, 1)
	go func() {
		close(cancelStarted)
		_, err := acquireAuditAppendGate(cancelled, repo, time.Second)
		cancelResult <- err
	}()
	<-cancelStarted
	cancel()
	select {
	case err := <-cancelResult:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancelled acquisition error = %v, want %v", err, context.Canceled)
		}
	case <-time.After(time.Second):
		t.Fatal("cancelled audit append gate acquisition did not return")
	}
	if _, err := acquireAuditAppendGate(context.Background(), repo, 0); err == nil || !strings.Contains(err.Error(), "must be positive") {
		t.Fatalf("non-positive timeout error = %v", err)
	}

	release()
	recovered, err := acquireAuditAppendGate(context.Background(), repo, time.Second)
	if err != nil {
		t.Fatalf("reacquire after timeout and cancellation: %v", err)
	}
	recovered()
	recovered()
	assertAuditAppendGatesEmpty(t)
}

func assertAuditAppendGatesEmpty(t *testing.T) {
	t.Helper()
	auditAppendGates.Lock()
	defer auditAppendGates.Unlock()
	if len(auditAppendGates.values) != 0 {
		t.Fatalf("idle audit append gates retained = %d, want 0", len(auditAppendGates.values))
	}
}

func TestAppendPublishesPrivateAuditLayoutAndMigratesLegacyModes(t *testing.T) {
	repo := t.TempDir()
	directory := filepath.Join(repo, ".reconc")
	if err := os.Mkdir(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := Append(repo, Entry{Event: "legacy", Decision: "pass"}, 0); err != nil {
		t.Fatal(err)
	}
	assertMode(t, directory, 0o700)
	for _, path := range []string{
		filepath.Join(repo, AuditFileRelative),
		filepath.Join(repo, AuditFileRelative+".lock"),
		filepath.Join(repo, AuditHeadRelative),
	} {
		assertMode(t, path, 0o600)
	}
	if err := os.Chmod(filepath.Join(repo, AuditFileRelative), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := Append(repo, Entry{Event: "migrated", Decision: "warn"}, 0); err != nil {
		t.Fatal(err)
	}
	assertMode(t, directory, 0o700)
	assertMode(t, filepath.Join(repo, AuditFileRelative), 0o600)
	if entries, err := Tail(repo, TailOptions{}); err != nil || len(entries) != 2 {
		t.Fatalf("migrated audit entries = %d, err=%v", len(entries), err)
	}
}

func TestAppendRejectsHostileAuditLockSymlink(t *testing.T) {
	repo := t.TempDir()
	directory := filepath.Join(repo, ".reconc")
	if err := os.Mkdir(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "outside.lock")
	if err := os.WriteFile(outside, []byte("outside"), 0o600); err != nil {
		t.Fatal(err)
	}
	lock := filepath.Join(repo, AuditFileRelative+".lock")
	if err := os.Symlink(outside, lock); err != nil {
		t.Fatal(err)
	}
	if err := Append(repo, Entry{Event: "blocked", Decision: "block"}, 0); err == nil || !strings.Contains(err.Error(), "non-symlink") {
		t.Fatalf("hostile lock result = %v", err)
	}
	body, err := os.ReadFile(outside)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "outside" {
		t.Fatal("hostile lock target was modified")
	}
	if _, err := os.Lstat(filepath.Join(repo, AuditFileRelative)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("audit live file was published despite hostile lock: %v", err)
	}
}

func TestInspectRetentionUsesPrivateAuditLayout(t *testing.T) {
	repo := t.TempDir()
	if err := Append(repo, Entry{Event: "inspect", Decision: "pass"}, 0); err != nil {
		t.Fatal(err)
	}
	live := filepath.Join(repo, AuditFileRelative)
	if err := os.Chmod(live, 0o644); err != nil {
		t.Fatal(err)
	}
	result, err := InspectRetention(repo)
	if err != nil {
		t.Fatalf("InspectRetention: %v", err)
	}
	if result != (jsonl.EnforceResult{}) {
		t.Fatalf("InspectRetention result = %+v, want no cleanup", result)
	}
	assertMode(t, live, 0o600)
}

func assertMode(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != want.Perm() {
		t.Fatalf("%s mode = %04o, want %04o", path, info.Mode().Perm(), want.Perm())
	}
}
