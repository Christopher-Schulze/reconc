package jsonl

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestAppendCreationRaceReopensStrictExistingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.jsonl")
	err := appendRecordWithLayoutHooks(path, []byte("record\n"), defaultLayout(path), 1024, appendOpenHooks{
		afterInspect: func(missing bool) error {
			if !missing {
				return errors.New("JSONL file unexpectedly existed before creation race")
			}
			return os.WriteFile(path, []byte("winner\n"), 0o644)
		},
	})
	if err != nil {
		t.Fatalf("reopen existing creation winner: %v", err)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(body, []byte("winner\nrecord\n")) {
		t.Fatalf("reopened JSONL body = %q", body)
	}
}

func TestAppendCreationRaceRejectsDanglingSymlinkWithoutCreatingTarget(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "events.jsonl")
	target := filepath.Join(root, "outside-target")
	var symlinkErr error
	err := appendRecordWithLayoutHooks(path, []byte("record\n"), defaultLayout(path), 1024, appendOpenHooks{
		afterInspect: func(missing bool) error {
			if !missing {
				return errors.New("JSONL file unexpectedly existed before symlink race")
			}
			symlinkErr = os.Symlink(target, path)
			return symlinkErr
		},
	})
	if symlinkErr != nil {
		t.Skipf("symlink creation is unavailable: %v", symlinkErr)
	}
	if err == nil {
		t.Fatal("dangling JSONL symlink won the create-only open")
	}
	if _, err := os.Lstat(target); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("rejected dangling symlink created its target: %v", err)
	}
}

func TestAppendCreationRaceRejectsHardLinkWithoutModifyingTarget(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "events.jsonl")
	victim := filepath.Join(root, "victim")
	want := []byte("victim-content")
	if err := os.WriteFile(victim, want, 0o644); err != nil {
		t.Fatal(err)
	}
	err := appendRecordWithLayoutHooks(path, []byte("record\n"), defaultLayout(path), 1024, appendOpenHooks{
		afterInspect: func(missing bool) error {
			if !missing {
				return errors.New("JSONL file unexpectedly existed before hard-link race")
			}
			return os.Link(victim, path)
		},
	})
	if err == nil {
		t.Fatal("hard-linked creation winner was accepted as a JSONL live file")
	}
	body, err := os.ReadFile(victim)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(body, want) {
		t.Fatalf("rejected hard link modified victim: %q", body)
	}
}

func TestAppendExistingReplacementCannotRedirectWrite(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "events.jsonl")
	if err := os.WriteFile(path, []byte("original\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	victim := filepath.Join(root, "victim")
	want := []byte("replacement-victim")
	if err := os.WriteFile(victim, want, 0o644); err != nil {
		t.Fatal(err)
	}
	err := appendRecordWithLayoutHooks(path, []byte("record\n"), defaultLayout(path), 1024, appendOpenHooks{
		afterInspect: func(missing bool) error {
			if missing {
				return errors.New("existing JSONL file disappeared before replacement race")
			}
			if err := os.Rename(path, path+".original"); err != nil {
				return err
			}
			return os.Link(victim, path)
		},
	})
	if err == nil {
		t.Fatal("replacement JSONL identity was accepted")
	}
	body, err := os.ReadFile(victim)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(body, want) {
		t.Fatalf("replacement race modified victim: %q", body)
	}
}

func TestAppendParentReplacementCannotRedirectCreation(t *testing.T) {
	root := t.TempDir()
	directory := filepath.Join(root, "state")
	moved := filepath.Join(root, "state-moved")
	outside := filepath.Join(root, "outside")
	if err := os.Mkdir(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(outside, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(directory, "events.jsonl")
	var symlinkErr error
	err := appendRecordWithLayoutHooks(path, []byte("record\n"), defaultLayout(path), 1024, appendOpenHooks{
		afterInspect: func(missing bool) error {
			if !missing {
				return errors.New("JSONL file unexpectedly existed before parent replacement")
			}
			if err := os.Rename(directory, moved); err != nil {
				return err
			}
			symlinkErr = os.Symlink(outside, directory)
			return symlinkErr
		},
	})
	if symlinkErr != nil {
		t.Skipf("symlink creation is unavailable: %v", symlinkErr)
	}
	if err == nil {
		t.Fatal("replaced JSONL parent was accepted")
	}
	for _, candidate := range []string{filepath.Join(outside, "events.jsonl"), filepath.Join(moved, "events.jsonl")} {
		if _, err := os.Lstat(candidate); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("rejected parent replacement left %s: %v", candidate, err)
		}
	}
}
