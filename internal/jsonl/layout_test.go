package jsonl

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func privateTestLayout(path string) Layout {
	directory := filepath.Dir(path)
	return Layout{
		LockPath:      filepath.Join(directory, "ledger.lock"),
		JournalPath:   filepath.Join(directory, "ledger-transaction.json"),
		BackupPrefix:  filepath.Join(directory, "ledger-transaction-backup"),
		DirectoryMode: 0o700, FileMode: 0o600, JournalMode: 0o600,
	}
}

func privateTestDirectory(t *testing.T) string {
	t.Helper()
	directory := t.TempDir()
	if runtime.GOOS != "windows" {
		if err := os.Chmod(directory, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	return directory
}

func TestCustomLayoutUsesExactPathsAndPrivateModes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "action", "ledger.jsonl")
	layout := privateTestLayout(path)
	policy := Policy{MaxBytes: 64, MaxArchives: 2}
	commits := 0
	for _, record := range [][]byte{[]byte(`{"record":1}`), []byte(`{"record":2,"padding":"012345678901234567890123456789"}`)} {
		body := append([]byte(nil), record...)
		if err := AppendTransactionWithLayout(path, policy, layout, func() ([]byte, error) {
			return body, nil
		}, func() error {
			commits++
			return nil
		}); err != nil {
			t.Fatalf("AppendTransactionWithLayout() error = %v", err)
		}
	}
	if commits != 2 {
		t.Fatalf("commit count = %d, want 2", commits)
	}
	for _, candidate := range []string{path, layout.LockPath, path + ".1"} {
		info, err := os.Lstat(candidate)
		if err != nil {
			t.Fatalf("Lstat(%s): %v", candidate, err)
		}
		if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
			t.Fatalf("mode(%s) = %o, want 600", candidate, info.Mode().Perm())
		}
	}
	for _, forbidden := range []string{path + ".lock", path + ".append-transaction.json"} {
		if _, err := os.Lstat(forbidden); !os.IsNotExist(err) {
			t.Fatalf("unexpected default-layout artifact %s: %v", forbidden, err)
		}
	}
	if _, err := os.Lstat(layout.JournalPath); !os.IsNotExist(err) {
		t.Fatalf("resolved journal remains: %v", err)
	}
}

func TestCustomJournalRejectsDifferentLayout(t *testing.T) {
	path := filepath.Join(privateTestDirectory(t), "ledger.jsonl")
	layout := privateTestLayout(path)
	policy := Policy{MaxBytes: 64, MaxArchives: 2}
	if err := withLayoutLock(path, layout, func() error {
		_, err := beginAppendJournalWithLayout(path, policy, layout, false, true)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	foreign := layout
	foreign.LockPath = filepath.Join(filepath.Dir(path), "foreign.lock")
	err := RecoverWithLayout(path, foreign, func() error { return nil })
	if err == nil {
		t.Fatalf("RecoverWithLayout() accepted a journal from a different layout")
	}
	if !errors.Is(err, ErrLayoutMismatch) {
		t.Fatalf("RecoverWithLayout() error = %v, want ErrLayoutMismatch", err)
	}
	if !strings.Contains(err.Error(), layout.JournalPath) {
		t.Fatalf("RecoverWithLayout() error = %v, want journal path context", err)
	}
	if _, err := os.Lstat(layout.JournalPath); err != nil {
		t.Fatalf("foreign recovery changed the original journal: %v", err)
	}
}

func TestCustomLayoutRejectsSymlinkTargets(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation is not reliably available")
	}
	directory := privateTestDirectory(t)
	path := filepath.Join(directory, "ledger.jsonl")
	layout := privateTestLayout(path)
	target := filepath.Join(directory, "target")
	if err := os.WriteFile(target, []byte("unchanged"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, path); err != nil {
		t.Fatal(err)
	}
	if err := AppendWithLayout(path, []byte(`{"record":1}`), Policy{MaxBytes: 64, MaxArchives: 2}, layout); err == nil {
		t.Fatalf("AppendWithLayout() accepted a symlink live path")
	}
	body, err := os.ReadFile(target)
	if err != nil || string(body) != "unchanged" {
		t.Fatalf("symlink target changed: body=%q err=%v", body, err)
	}
}

func TestPreparedRollbackRejectsSymlinkReplacementWithoutChangingTarget(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation is not reliably available")
	}
	directory := privateTestDirectory(t)
	path := filepath.Join(directory, "ledger.jsonl")
	layout := privateTestLayout(path)
	policy := Policy{MaxBytes: 64, MaxArchives: 2}
	if err := os.WriteFile(path, []byte("original\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := withLayoutLock(path, layout, func() error {
		_, err := beginAppendJournalWithLayout(path, policy, layout, false, true)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(path, path+".moved"); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(directory, "target")
	if err := os.WriteFile(target, []byte("unchanged"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, path); err != nil {
		t.Fatal(err)
	}
	if err := RecoverWithLayout(path, layout, func() error { return nil }); err == nil {
		t.Fatal("RecoverWithLayout() accepted a symlink replacement")
	}
	body, err := os.ReadFile(target)
	if err != nil || string(body) != "unchanged" {
		t.Fatalf("rollback changed symlink target: body=%q err=%v", body, err)
	}
	if _, err := os.Lstat(layout.JournalPath); err != nil {
		t.Fatalf("failed rollback discarded recovery journal: %v", err)
	}
}

func TestCustomLayoutBoundsContendedLock(t *testing.T) {
	path := filepath.Join(privateTestDirectory(t), "ledger.jsonl")
	layout := privateTestLayout(path)
	layout.LockTimeout = 40 * time.Millisecond
	acquired := make(chan struct{})
	release := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- withLayoutLockContext(context.Background(), path, layout, func() error {
			close(acquired)
			<-release
			return nil
		})
	}()
	<-acquired
	started := time.Now()
	err := AppendTransactionContextWithLayout(
		context.Background(), path, Policy{MaxBytes: 64, MaxArchives: 2}, layout,
		func() ([]byte, error) { return []byte(`{"record":1}`), nil },
		func() error { return nil },
	)
	if err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("contended append error = %v, want timeout", err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("contended append blocked for %s", elapsed)
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestAcquireLayoutLockReturnsPermanentErrorWithoutRetryingForever(t *testing.T) {
	file, err := os.CreateTemp(t.TempDir(), "closed-lock")
	if err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	if _, err := acquireLayoutLock(context.Background(), file, 0); err == nil {
		t.Fatal("acquireLayoutLock unexpectedly accepted a closed file")
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("permanent lock failure retried for %s", elapsed)
	}
}

func TestCustomLayoutRejectsFilesystemRootAsLivePath(t *testing.T) {
	root := string(filepath.Separator)
	layout := Layout{
		LockPath:      filepath.Join(root, "ledger.lock"),
		JournalPath:   filepath.Join(root, "ledger-transaction.json"),
		BackupPrefix:  filepath.Join(root, "ledger-transaction-backup"),
		DirectoryMode: 0o700, FileMode: 0o600, JournalMode: 0o600,
	}
	if err := validateLayout(root, layout); err == nil {
		t.Fatal("filesystem root was accepted as a JSONL live path")
	}
}

func TestCustomLayoutRejectsUnsafeParentWithoutRepair(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permission contract")
	}
	directory := filepath.Join(t.TempDir(), "action")
	if err := os.Mkdir(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(directory, "ledger.jsonl")
	layout := privateTestLayout(path)
	if err := AppendWithLayout(path, []byte(`{"record":1}`), Policy{MaxBytes: 64, MaxArchives: 2}, layout); err == nil {
		t.Fatal("custom layout accepted an unsafe parent mode")
	}
	info, err := os.Lstat(directory)
	if err != nil || info.Mode().Perm() != 0o755 {
		t.Fatalf("rejected append repaired unsafe parent mode: %v, %v", info, err)
	}
	for _, candidate := range []string{path, layout.LockPath} {
		if _, err := os.Lstat(candidate); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("rejected append created %s: %v", candidate, err)
		}
	}
}

func TestCustomLayoutRejectsSymlinkParentWithoutChangingTarget(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation is not reliably available")
	}
	root := t.TempDir()
	target := filepath.Join(root, "target")
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatal(err)
	}
	directory := filepath.Join(root, "action")
	if err := os.Symlink(target, directory); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(directory, "ledger.jsonl")
	layout := privateTestLayout(path)
	if err := AppendWithLayout(path, []byte(`{"record":1}`), Policy{MaxBytes: 64, MaxArchives: 2}, layout); err == nil {
		t.Fatal("custom layout accepted a symlink parent")
	}
	entries, err := os.ReadDir(target)
	if err != nil || len(entries) != 0 {
		t.Fatalf("rejected append changed symlink target: entries=%v err=%v", entries, err)
	}
}

func TestExistingSnapshotNeverCreatesOrRepairsMissingLock(t *testing.T) {
	directory := privateTestDirectory(t)
	path := filepath.Join(directory, "ledger.jsonl")
	layout := privateTestLayout(path)
	read := func() error { return nil }
	if err := ReadExistingSnapshotContextWithLayout(context.Background(), path, layout, nil, read); err == nil {
		t.Fatal("existing snapshot accepted a missing lock")
	}
	if _, err := os.Lstat(layout.LockPath); !os.IsNotExist(err) {
		t.Fatalf("existing snapshot created a missing lock: %v", err)
	}
	if runtime.GOOS == "windows" {
		return
	}
	if err := os.WriteFile(layout.LockPath, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := ReadExistingSnapshotContextWithLayout(context.Background(), path, layout, nil, read); err == nil {
		t.Fatal("existing snapshot repaired an unsafe lock mode")
	}
	if runtime.GOOS != "windows" {
		info, err := os.Lstat(layout.LockPath)
		if err != nil || info.Mode().Perm() != 0o644 {
			t.Fatalf("existing snapshot changed lock mode: %v, %v", info, err)
		}
	}
}

func TestExistingLayoutLockSerializesInspectionWithoutRecovery(t *testing.T) {
	directory := privateTestDirectory(t)
	path := filepath.Join(directory, "ledger.jsonl")
	layout := privateTestLayout(path)
	if err := os.WriteFile(layout.LockPath, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	journalBody := []byte("not a recovery journal\n")
	if err := os.WriteFile(layout.JournalPath, journalBody, 0o600); err != nil {
		t.Fatal(err)
	}
	acquired := make(chan struct{})
	release := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- WithExistingLayoutLockContext(context.Background(), path, layout, func() error {
			close(acquired)
			<-release
			return nil
		})
	}()
	<-acquired
	contenderContext, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer cancel()
	called := false
	err := WithExistingLayoutLockContext(contenderContext, path, layout, func() error {
		called = true
		return nil
	})
	if err == nil || called || !strings.Contains(err.Error(), "context deadline exceeded") {
		t.Fatalf("contended existing inspection = called %v, error %v", called, err)
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(layout.JournalPath)
	if err != nil || !bytes.Equal(body, journalBody) {
		t.Fatalf("existing inspection recovered or changed the journal: body=%q err=%v", body, err)
	}
}

func TestPreparingRotationRecoveryCleansPartialBackupsWithoutChangingLiveData(t *testing.T) {
	directory := privateTestDirectory(t)
	path := filepath.Join(directory, "ledger.jsonl")
	layout := privateTestLayout(path)
	original := []byte("original\n")
	if err := os.WriteFile(path, original, 0o600); err != nil {
		t.Fatal(err)
	}
	journal := appendJournal{
		FormatVersion: appendJournalVersion, LayoutIdentity: layoutIdentity(path, layout),
		State: appendStatePreparing, Transactional: true, Rotated: true,
		MaxBytes: 64, MaxArchives: 2, LiveExisted: true, LiveSize: int64(len(original)),
		Backups: []appendJournalBackup{},
	}
	if err := writeAppendJournalWithLayout(path, layout, journal); err != nil {
		t.Fatal(err)
	}
	partialBackup := appendBackupPathWithLayout(layout, 0)
	if err := os.WriteFile(partialBackup, original, 0o600); err != nil {
		t.Fatal(err)
	}
	committed := false
	if err := RecoverWithLayout(path, layout, func() error {
		committed = true
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if committed {
		t.Fatal("preparing transaction invoked the commit callback")
	}
	body, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(body, original) {
		t.Fatalf("recovery changed live data: body=%q err=%v", body, err)
	}
	for _, candidate := range []string{layout.JournalPath, partialBackup} {
		if _, err := os.Lstat(candidate); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("preparing transaction artifact remains at %s: %v", candidate, err)
		}
	}
}

func TestCustomRecoveryRejectsUnsafeJournalModeWithoutRepair(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permission contract")
	}
	path := filepath.Join(privateTestDirectory(t), "ledger.jsonl")
	layout := privateTestLayout(path)
	if err := os.WriteFile(path, []byte("original\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := withLayoutLock(path, layout, func() error {
		_, err := beginAppendJournalWithLayout(path, Policy{MaxBytes: 64, MaxArchives: 2}, layout, false, true)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(layout.JournalPath, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := RecoverWithLayout(path, layout, func() error { return nil }); err == nil {
		t.Fatal("recovery accepted an unsafe transaction-journal mode")
	}
	info, err := os.Lstat(layout.JournalPath)
	if err != nil || info.Mode().Perm() != 0o644 {
		t.Fatalf("failed recovery repaired or removed the unsafe journal: %v, %v", info, err)
	}
}

func TestDefaultAppendPreservesExistingRestrictiveMode(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permission contract")
	}
	path := filepath.Join(t.TempDir(), "events.jsonl")
	if err := os.WriteFile(path, []byte("one\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Append(path, []byte("two"), Policy{MaxBytes: 64, MaxArchives: 1}); err != nil {
		t.Fatal(err)
	}
	info, err := os.Lstat(path)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("default append widened existing mode: %v, %v", info, err)
	}
}

func TestDefaultRotationPreservesExistingRestrictiveMode(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permission contract")
	}
	path := filepath.Join(t.TempDir(), "events.jsonl")
	if err := os.WriteFile(path, []byte("one\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Append(path, []byte("two-two"), Policy{MaxBytes: 8, MaxArchives: 1}); err != nil {
		t.Fatal(err)
	}
	for _, candidate := range []string{path, path + ".1"} {
		info, err := os.Lstat(candidate)
		if err != nil || info.Mode().Perm() != 0o600 {
			t.Fatalf("default rotation changed mode for %s: %v, %v", candidate, info, err)
		}
	}
}
