package jsonl

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
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

func TestArchiveCandidatesIgnoreUnrelatedAndInvalidSuffixes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.jsonl")
	for _, candidate := range []string{
		path + ".1",
		path + ".0",
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

func TestWithLockPropagatesOperationFailure(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.jsonl")
	want := errors.New("operation failed")
	err := withLock(path, func() error { return want })
	if !errors.Is(err, want) {
		t.Fatalf("withLock error = %v, want %v", err, want)
	}
}

func TestTailDataKeepsOnlyCompleteRecords(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.jsonl")
	if err := os.WriteFile(path, []byte("first\nsecond\npartial"), 0o640); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	original, kept, body, mode, err := tailData(path, 15)
	if err != nil {
		t.Fatalf("tailData: %v", err)
	}
	if original != 20 || kept != 7 || string(body) != "second\n" {
		t.Fatalf("tailData = original %d, kept %d, body %q", original, kept, body)
	}
	if mode != 0o640 {
		t.Fatalf("mode = %o, want 640", mode)
	}
}
