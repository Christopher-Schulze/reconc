package jsonl

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestJSONLRotationDurabilityCrashPointsRecoverIdempotently(t *testing.T) {
	policy := Policy{MaxBytes: 64, MaxArchives: 3}
	want := map[int]string{
		0: "live\n",
		1: "archive-one\n",
		2: "archive-two\n",
		3: "archive-three\n",
	}
	for failAt := 1; failAt <= 4; failAt++ {
		t.Run(fmt.Sprintf("mutation-%d", failAt), func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "events.jsonl")
			for index, body := range want {
				if err := os.WriteFile(archivePath(path, index), []byte(body), 0o640); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := beginAppendJournal(path, policy, true, false); err != nil {
				t.Fatal(err)
			}
			original := jsonlDirectorySync
			syncs := 0
			jsonlDirectorySync = func(directory *os.Root) error {
				syncs++
				return original(directory)
			}
			t.Cleanup(func() { jsonlDirectorySync = original })
			injected := errors.New("injected post-rotation crash")
			err := rotateWithHooks(path, policy.MaxArchives, rotationHooks{
				afterMutation: func(mutation int) error {
					if syncs != mutation {
						return fmt.Errorf("rotation mutation %d has %d durability barriers", mutation, syncs)
					}
					if mutation == failAt {
						return injected
					}
					return nil
				},
			})
			if !errors.Is(err, injected) {
				t.Fatalf("rotation crash %d error = %v", failAt, err)
			}
			if err := Recover(path, nil); err != nil {
				t.Fatalf("recover crash %d: %v", failAt, err)
			}
			if err := Recover(path, nil); err != nil {
				t.Fatalf("idempotent recovery crash %d: %v", failAt, err)
			}
			for index, body := range want {
				got, err := os.ReadFile(archivePath(path, index))
				if err != nil || string(got) != body {
					t.Fatalf("crash %d archive %d = %q, %v", failAt, index, got, err)
				}
			}
			if _, err := os.Lstat(appendJournalPath(path)); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("crash %d journal remains: %v", failAt, err)
			}
			for index := range want {
				if _, err := os.Lstat(appendBackupPath(path, index)); !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("crash %d backup %d remains: %v", failAt, index, err)
				}
			}
		})
	}
}

func TestJSONLLiveCreationRequiresParentDurability(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.jsonl")
	original := jsonlDirectorySync
	calls := 0
	jsonlDirectorySync = func(*os.Root) error {
		calls++
		return nil
	}
	t.Cleanup(func() { jsonlDirectorySync = original })
	if err := Append(path, []byte("created"), Policy{MaxBytes: 64, MaxArchives: 1}); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("live-file parent durability barriers = %d, want 1", calls)
	}
	body, err := os.ReadFile(path)
	if err != nil || string(body) != "created\n" {
		t.Fatalf("created live file = %q, %v", body, err)
	}

	jsonlDirectorySync = func(*os.Root) error {
		return errors.New("injected live-file parent sync failure")
	}
	failed := filepath.Join(t.TempDir(), "failed.jsonl")
	if err := Append(failed, []byte("complete"), Policy{MaxBytes: 64, MaxArchives: 1}); err == nil ||
		!strings.Contains(err.Error(), "injected live-file parent sync failure") {
		t.Fatalf("live-file sync failure = %v", err)
	}
	body, err = os.ReadFile(failed)
	if err != nil || string(body) != "complete\n" {
		t.Fatalf("failed live publication exposed partial bytes: %q, %v", body, err)
	}
}
