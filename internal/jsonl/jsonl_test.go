package jsonl

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
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
