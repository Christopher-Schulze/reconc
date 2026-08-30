package jsonl

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"reconc.dev/reconc/internal/boundedio"
)

func TestPolicyValidationAcrossPublicOperations(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.jsonl")
	for _, test := range []struct {
		name   string
		policy Policy
	}{
		{name: "zero byte budget", policy: Policy{MaxBytes: 0, MaxArchives: 1}},
		{name: "negative archives", policy: Policy{MaxBytes: 64, MaxArchives: -1}},
		{name: "excess archives", policy: Policy{MaxBytes: 64, MaxArchives: 33}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := Inspect(path, test.policy); err == nil {
				t.Fatal("Inspect accepted invalid policy")
			}
			if err := Append(path, []byte("{}"), test.policy); err == nil {
				t.Fatal("Append accepted invalid policy")
			}
			if _, err := Enforce(path, test.policy); err == nil {
				t.Fatal("Enforce accepted invalid policy")
			}
		})
	}
}

func TestAppendRejectsOversizedRecordWithoutCreatingState(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "events.jsonl")
	err := Append(path, []byte("12345678"), Policy{MaxBytes: 8, MaxArchives: 1})
	if err == nil || !strings.Contains(err.Error(), "jsonl record is 9 bytes") {
		t.Fatalf("expected exact oversized-record error, got %v", err)
	}
	if _, statErr := os.Stat(path); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("oversized record created live file: %v", statErr)
	}
	if _, statErr := os.Stat(path + ".lock"); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("oversized record created lock file: %v", statErr)
	}
}

func TestAppendRejectsInvalidRecordFramingWithoutCreatingState(t *testing.T) {
	for _, test := range []struct {
		name   string
		record string
	}{
		{name: "empty"},
		{name: "spaces", record: " \t "},
		{name: "empty LF", record: "\n"},
		{name: "empty CRLF", record: "\r\n"},
		{name: "embedded CR", record: "first\rsecond"},
		{name: "embedded LF", record: "first\nsecond"},
		{name: "embedded CRLF", record: "first\r\nsecond"},
		{name: "multiple terminators", record: "record\r\n\r\n"},
	} {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "events.jsonl")
			err := Append(path, []byte(test.record), Policy{MaxBytes: 64, MaxArchives: 1})
			if err == nil {
				t.Fatal("Append accepted invalid record framing")
			}
			for _, candidate := range []string{path, path + ".lock", path + ".append-transaction.json"} {
				if _, statErr := os.Lstat(candidate); !errors.Is(statErr, os.ErrNotExist) {
					t.Fatalf("invalid record created %s: %v", candidate, statErr)
				}
			}
		})
	}
}

func TestAppendTransactionRejectsInvalidRecordBeforeMutation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.jsonl")
	want := []byte("existing\n")
	if err := os.WriteFile(path, want, 0o600); err != nil {
		t.Fatal(err)
	}
	committed := false
	err := AppendTransaction(path, Policy{MaxBytes: 64, MaxArchives: 1}, func() ([]byte, error) {
		return []byte("forged\nrecord"), nil
	}, func() error {
		committed = true
		return nil
	})
	if err == nil || !strings.Contains(err.Error(), "must not contain CR or LF") {
		t.Fatalf("transaction framing error = %v", err)
	}
	if committed {
		t.Fatal("invalid record advanced the transaction commit")
	}
	body, readErr := os.ReadFile(path)
	if readErr != nil || !reflect.DeepEqual(body, want) {
		t.Fatalf("invalid transaction changed live JSONL: body=%q err=%v", body, readErr)
	}
	for _, candidate := range []string{path + ".append-transaction.json", path + ".append-backup.0"} {
		if _, statErr := os.Lstat(candidate); !errors.Is(statErr, os.ErrNotExist) {
			t.Fatalf("invalid transaction created %s: %v", candidate, statErr)
		}
	}
}

func TestAppendWithNoArchivesReplacesFullLiveFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.jsonl")
	if err := os.WriteFile(path, []byte("old-record\n"), 0o644); err != nil {
		t.Fatalf("write live fixture: %v", err)
	}
	if err := Append(path, []byte("new"), Policy{MaxBytes: 11, MaxArchives: 0}); err != nil {
		t.Fatalf("Append: %v", err)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read live file: %v", err)
	}
	if string(body) != "new\n" {
		t.Fatalf("live file was not replaced: %q", body)
	}
	if _, err := os.Stat(path + ".1"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("zero-archive policy created archive: %v", err)
	}
}

func TestPathsOldestFirstPreservesChronology(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.jsonl")
	for _, suffix := range []string{"", ".1", ".3"} {
		if err := os.WriteFile(path+suffix, []byte(suffix), 0o600); err != nil {
			t.Fatalf("write %q: %v", suffix, err)
		}
	}
	got, err := PathsOldestFirst(path, 3)
	if err != nil {
		t.Fatalf("PathsOldestFirst: %v", err)
	}
	want := []string{path + ".3", path + ".1", path}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("paths = %v, want %v", got, want)
	}
}

func TestRingSizeRejectsSymlinkWithoutCountingTargetBytes(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "events.jsonl")
	if err := os.WriteFile(path, []byte("live\n"), 0o600); err != nil {
		t.Fatalf("write live file: %v", err)
	}
	if err := os.WriteFile(path+".1", []byte("archive\n"), 0o600); err != nil {
		t.Fatalf("write archive file: %v", err)
	}
	target := filepath.Join(root, "foreign-target.jsonl")
	if err := os.WriteFile(target, make([]byte, 1<<20), 0o600); err != nil {
		t.Fatalf("write target file: %v", err)
	}
	if err := os.Symlink(target, path+".2"); err != nil {
		t.Skipf("symlink creation unavailable: %v", err)
	}
	bytes, files, err := RingSize(path, 2)
	if err == nil || !strings.Contains(err.Error(), "non-symlink regular") {
		t.Fatalf("RingSize accepted linked archive: bytes=%d files=%d err=%v", bytes, files, err)
	}
	if bytes != 0 || files != 0 {
		t.Fatalf("RingSize published partial or target bytes after rejection: bytes=%d files=%d", bytes, files)
	}
	if _, err := Inspect(path, Policy{MaxBytes: 1024, MaxArchives: 1}); err == nil {
		t.Fatal("Inspect accepted linked archive")
	}
	if _, err := Enforce(path, Policy{MaxBytes: 1024, MaxArchives: 1}); err == nil {
		t.Fatal("Enforce accepted linked archive")
	}
	if _, err := os.Lstat(path + ".2"); err != nil {
		t.Fatalf("linked archive was removed after rejection: %v", err)
	}
}

func TestRingSizeAcceptsSparseRingAtContractLimit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.jsonl")
	if err := os.WriteFile(path+fmt.Sprintf(".%d", MaxArchiveFiles), []byte("last\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	bytes, files, err := RingSize(path, MaxArchiveFiles)
	if err != nil || bytes != int64(len("last\n")) || files != 1 {
		t.Fatalf("sparse ring = bytes %d files %d err %v", bytes, files, err)
	}
	if _, _, err := RingSize(path, MaxArchiveFiles+1); err == nil {
		t.Fatal("RingSize accepted a bound outside the JSONL contract")
	}
}

func TestPathsOldestFirstRejectsArchiveOutsideContract(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.jsonl")
	extra := fmt.Sprintf("%s.%d", path, MaxArchiveFiles+1)
	if err := os.WriteFile(extra, []byte("outside\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := PathsOldestFirst(path, MaxArchiveFiles); err == nil || !strings.Contains(err.Error(), "exceeds bound") {
		t.Fatalf("out-of-contract archive was accepted: %v", err)
	}
}

func TestArchiveCandidatesIgnoreUnrelatedAndInvalidSuffixes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.jsonl")
	for _, candidate := range []string{
		path + ".1",
		path + ".0",
		path + ".0002",
		path + ".+2",
		path + ".invalid",
		filepath.Join(filepath.Dir(path), "other.jsonl.2"),
	} {
		if err := os.WriteFile(candidate, []byte("{}\n"), 0o600); err != nil {
			t.Fatalf("write fixture %s: %v", candidate, err)
		}
	}
	got, err := archiveCandidates(path)
	if err != nil {
		t.Fatalf("archiveCandidates: %v", err)
	}
	if len(got) != 1 || got[0].index != 1 || got[0].path != path+".1" {
		t.Fatalf("unexpected archive candidates: %+v", got)
	}
}

func TestReadArchiveDirectoryRetriesTransientSnapshotChange(t *testing.T) {
	directory := t.TempDir()
	const transientFailures = 3
	calls := 0
	entries, err := readArchiveDirectoryWith(directory, func(path string, maximum int) ([]os.DirEntry, error) {
		calls++
		if calls <= transientFailures {
			return nil, fmt.Errorf("transient archive directory churn: %w", boundedio.ErrDirectorySnapshotChanged)
		}
		return boundedio.ReadDir(path, maximum)
	})
	if err != nil {
		t.Fatal(err)
	}
	if entries == nil || calls != transientFailures+1 {
		t.Fatalf("archive directory retry calls = %d, entries = %v", calls, entries)
	}
}

func TestWithLayoutLockPropagatesOperationFailure(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.jsonl")
	want := errors.New("operation failed")
	err := withLayoutLock(path, defaultLayout(path), func() error { return want })
	if !errors.Is(err, want) {
		t.Fatalf("withLayoutLock error = %v, want %v", err, want)
	}
}

func TestTailDataKeepsOnlyCompleteRecords(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.jsonl")
	if err := os.WriteFile(path, []byte("first\nsecond\npartial"), 0o640); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat fixture: %v", err)
	}
	original, kept, body, mode, err := tailData(path, 15)
	if err != nil {
		t.Fatalf("tailData: %v", err)
	}
	if original != 20 || kept != 7 || string(body) != "second\n" {
		t.Fatalf("tailData = original %d, kept %d, body %q", original, kept, body)
	}
	if mode != info.Mode().Perm() {
		t.Fatalf("mode = %o, want source mode %o", mode, info.Mode().Perm())
	}
}

func TestTailDataKeepsRecordStartingAtWindowBoundary(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.jsonl")
	if err := os.WriteFile(path, []byte("old\nkeep\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	original, kept, body, _, err := tailData(path, 5)
	if err != nil {
		t.Fatal(err)
	}
	if original != 9 || kept != 5 || string(body) != "keep\n" {
		t.Fatalf("tailData = original %d, kept %d, body %q", original, kept, body)
	}
}

func TestEnforcePreservesFileWithoutCompleteRetainedRecord(t *testing.T) {
	for _, body := range [][]byte{
		[]byte("one-oversized-record"),
		[]byte("one-oversized-record\n"),
		[]byte("old\noversized-partial-record"),
	} {
		t.Run(fmt.Sprintf("bytes-%d-terminal-%t", len(body), body[len(body)-1] == '\n'), func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "events.jsonl")
			if err := os.WriteFile(path, body, 0o600); err != nil {
				t.Fatal(err)
			}
			_, err := Enforce(path, Policy{MaxBytes: 8, MaxArchives: 1})
			if !errors.Is(err, ErrNoCompleteRecordWithinLimit) {
				t.Fatalf("Enforce error = %v", err)
			}
			got, readErr := os.ReadFile(path)
			if readErr != nil || !reflect.DeepEqual(got, body) {
				t.Fatalf("failed retention changed source: got %q, err %v", got, readErr)
			}
		})
	}
}
