package jsonl

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
)

func TestAppendBoundedUnderConcurrency(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.jsonl")
	policy := Policy{MaxBytes: 2048, MaxArchives: 2}
	const workers = 24
	var wait sync.WaitGroup
	errors := make(chan error, workers)
	for worker := 0; worker < workers; worker++ {
		worker := worker
		wait.Add(1)
		go func() {
			defer wait.Done()
			for item := 0; item < 20; item++ {
				line, _ := json.Marshal(map[string]int{"worker": worker, "item": item})
				if err := Append(path, line, policy); err != nil {
					errors <- err
					return
				}
			}
		}()
	}
	wait.Wait()
	close(errors)
	for err := range errors {
		t.Fatal(err)
	}
	paths, err := PathsOldestFirst(path, policy.MaxArchives)
	if err != nil {
		t.Fatal(err)
	}
	for _, source := range paths {
		info, err := os.Stat(source)
		if err != nil {
			t.Fatal(err)
		}
		if info.Size() > policy.MaxBytes {
			t.Fatalf("unbounded %s: size=%d", source, info.Size())
		}
		file, err := os.Open(source)
		if err != nil {
			t.Fatal(err)
		}
		scanner := bufio.NewScanner(file)
		for scanner.Scan() {
			var value map[string]int
			if err := json.Unmarshal(scanner.Bytes(), &value); err != nil {
				t.Fatalf("torn record in %s: %v", source, err)
			}
		}
		_ = file.Close()
		if err := scanner.Err(); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := os.Stat(path + ".3"); !os.IsNotExist(err) {
		t.Fatalf("archive ring escaped bound: %v", err)
	}
}

func TestEnforceCompactsLegacyFilesAndArchives(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.jsonl")
	for index := 0; index <= 4; index++ {
		candidate := path
		if index > 0 {
			candidate = fmt.Sprintf("%s.%d", path, index)
		}
		var body []byte
		for line := 0; line < 100; line++ {
			body = append(body, fmt.Sprintf("{\"line\":%d}\n", line)...)
		}
		if err := os.WriteFile(candidate, body, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	result, err := Enforce(path, Policy{MaxBytes: 256, MaxArchives: 2})
	if err != nil {
		t.Fatal(err)
	}
	if result.BytesFreed == 0 || result.FilesRemoved != 2 {
		t.Fatalf("unexpected enforce result: %+v", result)
	}
	paths, err := PathsOldestFirst(path, 2)
	if err != nil {
		t.Fatal(err)
	}
	for _, source := range paths {
		info, err := os.Stat(source)
		if err != nil {
			t.Fatal(err)
		}
		if info.Size() > 256 {
			t.Fatalf("legacy file not compacted: %s size=%d", source, info.Size())
		}
	}
}

func TestAppendCompactsLegacyFilesBeforeRotation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.jsonl")
	legacy := repeatedLine(200)
	if err := os.WriteFile(path, legacy, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path+".1", legacy, 0o644); err != nil {
		t.Fatal(err)
	}
	policy := Policy{MaxBytes: 256, MaxArchives: 2}
	if err := Append(path, []byte(`{"new":true}`), policy); err != nil {
		t.Fatal(err)
	}
	paths, err := PathsOldestFirst(path, policy.MaxArchives)
	if err != nil {
		t.Fatal(err)
	}
	for _, source := range paths {
		info, err := os.Stat(source)
		if err != nil || info.Size() > policy.MaxBytes {
			t.Fatalf("legacy rotation escaped bound for %s: info=%v err=%v", source, info, err)
		}
	}
}

func TestRecoverRollsBackPreparedRotation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.jsonl")
	policy := Policy{MaxBytes: 64, MaxArchives: 2}
	original := map[string][]byte{
		path:        []byte("live-old\n"),
		path + ".1": []byte("archive-one\n"),
		path + ".2": []byte("archive-two\n"),
	}
	for target, body := range original {
		if err := os.WriteFile(target, body, 0o640); err != nil {
			t.Fatal(err)
		}
	}
	err := withLock(path, func() error {
		if err := prepareRotationInputs(path, policy.MaxArchives, policy.MaxBytes); err != nil {
			return err
		}
		if _, err := beginAppendJournal(path, policy, true, true); err != nil {
			return err
		}
		if err := rotate(path, policy.MaxArchives); err != nil {
			return err
		}
		return appendRecord(path, []byte("partially-published\n"))
	})
	if err != nil {
		t.Fatal(err)
	}
	commitCalled := false
	if err := Recover(path, func() error {
		commitCalled = true
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if commitCalled {
		t.Fatal("prepared transaction invoked commit instead of rolling back")
	}
	for target, want := range original {
		got, err := os.ReadFile(target)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("rollback mismatch for %s: got %q want %q", target, got, want)
		}
	}
	assertNoAppendJournal(t, path, policy.MaxArchives)
}

func TestAppendTransactionRecoversPublishedRotation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.jsonl")
	policy := Policy{MaxBytes: 24, MaxArchives: 2}
	for _, record := range [][]byte{[]byte("old-one"), []byte("old-two")} {
		if err := Append(path, record, policy); err != nil {
			t.Fatal(err)
		}
	}
	commitCalls := 0
	commit := func() error {
		commitCalls++
		if commitCalls == 1 {
			return errors.New("injected detached-head failure")
		}
		return nil
	}
	if err := AppendTransaction(path, policy, func() ([]byte, error) {
		return []byte("published"), nil
	}, commit); err == nil {
		t.Fatal("expected injected commit failure")
	}
	if _, err := os.Stat(appendJournalPath(path)); err != nil {
		t.Fatalf("published transaction did not retain recovery journal: %v", err)
	}
	if err := AppendTransaction(path, policy, func() ([]byte, error) {
		return []byte("after-recovery"), nil
	}, commit); err != nil {
		t.Fatal(err)
	}
	if commitCalls != 3 {
		t.Fatalf("commit calls = %d, want recovery plus two publication attempts", commitCalls)
	}
	paths, err := PathsOldestFirst(path, policy.MaxArchives)
	if err != nil {
		t.Fatal(err)
	}
	var combined []byte
	for _, source := range paths {
		body, err := os.ReadFile(source)
		if err != nil {
			t.Fatal(err)
		}
		combined = append(combined, body...)
	}
	if !bytes.Contains(combined, []byte("published\n")) || !bytes.Contains(combined, []byte("after-recovery\n")) {
		t.Fatalf("recovered records are missing: %q", combined)
	}
	assertNoAppendJournal(t, path, policy.MaxArchives)
}

func TestAppendJournalValidationFailsClosed(t *testing.T) {
	valid := appendJournal{
		FormatVersion: appendJournalVersion,
		State:         appendStatePrepared,
		MaxBytes:      64,
		MaxArchives:   1,
	}
	tests := []struct {
		name   string
		mutate func(*appendJournal)
	}{
		{name: "version", mutate: func(j *appendJournal) { j.FormatVersion++ }},
		{name: "state", mutate: func(j *appendJournal) { j.State = "unknown" }},
		{name: "policy", mutate: func(j *appendJournal) { j.MaxBytes = 0 }},
		{name: "live size", mutate: func(j *appendJournal) { j.LiveSize = -1 }},
		{name: "unexpected backups", mutate: func(j *appendJournal) { j.Backups = []appendJournalBackup{{Index: 0}} }},
		{name: "missing backups", mutate: func(j *appendJournal) { j.Rotated = true }},
		{name: "backup index", mutate: func(j *appendJournal) {
			j.Rotated = true
			j.Backups = []appendJournalBackup{{Index: 1}, {Index: 2}}
		}},
		{name: "backup mode", mutate: func(j *appendJournal) {
			j.Rotated = true
			j.Backups = []appendJournalBackup{{Index: 0, Existed: true, Mode: 0o1000, Digest: strings.Repeat("a", 64)}, {Index: 1}}
		}},
		{name: "backup digest", mutate: func(j *appendJournal) {
			j.Rotated = true
			j.Backups = []appendJournalBackup{{Index: 0, Existed: true, Mode: 0o600, Digest: "bad"}, {Index: 1}}
		}},
		{name: "absent metadata", mutate: func(j *appendJournal) {
			j.Rotated = true
			j.Backups = []appendJournalBackup{{Index: 0, Mode: 0o600}, {Index: 1}}
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			journal := valid
			test.mutate(&journal)
			if err := validateAppendJournal(journal); err == nil {
				t.Fatalf("invalid journal was accepted: %#v", journal)
			}
		})
	}
}

func TestReadAppendJournalRejectsMalformedAndOversizedData(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.jsonl")
	for _, body := range [][]byte{
		[]byte("{} trailing"),
		append([]byte(`{"format_version":1}`), make([]byte, maxAppendJournalBytes)...),
	} {
		if err := os.WriteFile(appendJournalPath(path), body, 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := readAppendJournal(path); err == nil {
			t.Fatalf("invalid journal was accepted (%d bytes)", len(body))
		}
	}
}

func TestRecoverPreparedAppendWithoutRotation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.jsonl")
	policy := Policy{MaxBytes: 64, MaxArchives: 1}
	if err := os.WriteFile(path, []byte("before\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	err := withLock(path, func() error {
		if _, err := beginAppendJournal(path, policy, false, true); err != nil {
			return err
		}
		return appendRecord(path, []byte("torn\n"))
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := Recover(path, nil); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(path)
	if err != nil || string(body) != "before\n" {
		t.Fatalf("prepared append was not truncated: body=%q err=%v", body, err)
	}
	assertNoAppendJournal(t, path, policy.MaxArchives)
}

func TestRecoverWithoutJournalDoesNotCreateState(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "missing", "events.jsonl")
	if err := Recover(path, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Dir(path)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("read-only recovery created state: %v", err)
	}
}

func TestRecoverPublishedTransactionRequiresCommitCallback(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.jsonl")
	journal := appendJournal{
		FormatVersion: appendJournalVersion,
		State:         appendStatePublished,
		Transactional: true,
		MaxBytes:      64,
		MaxArchives:   1,
	}
	if err := writeAppendJournal(path, journal); err != nil {
		t.Fatal(err)
	}
	if err := Recover(path, nil); err == nil || !strings.Contains(err.Error(), "commit callback") {
		t.Fatalf("published transaction without commit callback = %v", err)
	}
}

func TestRecoverResolvedJournalOnlyCleansArtifacts(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.jsonl")
	journal := appendJournal{
		FormatVersion: appendJournalVersion,
		State:         appendStateResolved,
		Rotated:       true,
		MaxBytes:      64,
		MaxArchives:   0,
		Backups:       []appendJournalBackup{{Index: 0}},
	}
	if err := os.WriteFile(appendBackupPath(path, 0), []byte("orphan"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := writeAppendJournal(path, journal); err != nil {
		t.Fatal(err)
	}
	commitCalled := false
	if err := Recover(path, func() error {
		commitCalled = true
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if commitCalled {
		t.Fatal("resolved journal reran commit")
	}
	assertNoAppendJournal(t, path, journal.MaxArchives)
}

func TestRecoverRejectsCorruptBackup(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.jsonl")
	policy := Policy{MaxBytes: 64, MaxArchives: 0}
	if err := os.WriteFile(path, []byte("before\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	err := withLock(path, func() error {
		if _, err := beginAppendJournal(path, policy, true, true); err != nil {
			return err
		}
		return rotate(path, policy.MaxArchives)
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(appendBackupPath(path, 0), []byte("tampered\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Recover(path, func() error { return nil }); err == nil || !strings.Contains(err.Error(), "digest mismatch") {
		t.Fatalf("corrupt backup recovery = %v", err)
	}
	if _, err := os.Stat(appendJournalPath(path)); err != nil {
		t.Fatalf("failed recovery discarded its journal: %v", err)
	}
}

func TestRollbackAppendErrorPreservesCauseAndRestoresState(t *testing.T) {
	cause := errors.New("append failed")
	if got := rollbackAppendError("unused", nil, cause); !errors.Is(got, cause) {
		t.Fatalf("nil-journal rollback lost cause: %v", got)
	}
	path := filepath.Join(t.TempDir(), "events.jsonl")
	policy := Policy{MaxBytes: 64, MaxArchives: 0}
	journal, err := beginAppendJournal(path, policy, false, true)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("partial\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := rollbackAppendError(path, &journal, cause); !errors.Is(got, cause) {
		t.Fatalf("rollback lost cause: %v", got)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("rollback retained newly created live file: %v", err)
	}
	assertNoAppendJournal(t, path, policy.MaxArchives)
}

func assertNoAppendJournal(t *testing.T, path string, maxArchives int) {
	t.Helper()
	for _, candidate := range append([]string{appendJournalPath(path)}, func() []string {
		paths := make([]string, 0, maxArchives+1)
		for index := 0; index <= maxArchives; index++ {
			paths = append(paths, appendBackupPath(path, index))
		}
		return paths
	}()...) {
		if _, err := os.Stat(candidate); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("transaction artifact remains at %s: %v", candidate, err)
		}
	}
}

func TestPathsOldestFirstRejectsInvalidArchiveBound(t *testing.T) {
	if _, err := PathsOldestFirst(filepath.Join(t.TempDir(), "events.jsonl"), 33); err == nil {
		t.Fatal("expected invalid archive bound to fail")
	}
}

func TestInspectMatchesEnforceWithoutMutation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.jsonl")
	body := repeatedLine(100)
	for index := 0; index <= 3; index++ {
		candidate := path
		if index > 0 {
			candidate = fmt.Sprintf("%s.%d", path, index)
		}
		if err := os.WriteFile(candidate, body, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	policy := Policy{MaxBytes: 256, MaxArchives: 1}
	planned, err := Inspect(path, policy)
	if err != nil {
		t.Fatal(err)
	}
	afterInspect, err := os.ReadFile(path)
	if err != nil || string(afterInspect) != string(before) {
		t.Fatalf("Inspect mutated live file: err=%v", err)
	}
	actual, err := Enforce(path, policy)
	if err != nil {
		t.Fatal(err)
	}
	if planned != actual {
		t.Fatalf("inspect/enforce drift: planned=%+v actual=%+v", planned, actual)
	}
}

func repeatedLine(count int) []byte {
	var body []byte
	for index := 0; index < count; index++ {
		body = append(body, fmt.Sprintf("{\"line\":%d}\n", index)...)
	}
	return body
}
