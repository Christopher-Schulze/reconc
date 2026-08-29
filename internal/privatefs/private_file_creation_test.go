package privatefs

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestOpenLockCreationRaceReopensStrictExistingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "private", "state.lock")
	file, err := openPrivateFileWithHooks(path, true, true, privateFileOpenHooks{
		afterInspect: func(missing bool) error {
			if !missing {
				return errors.New("lock unexpectedly existed before creation race")
			}
			return os.WriteFile(path, nil, PrivateFileMode)
		},
	})
	if err != nil {
		t.Fatalf("reopen existing creation winner: %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	file, err = OpenExistingLock(path)
	if err != nil {
		t.Fatalf("validate creation winner: %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestOpenLockCreationRaceRejectsDanglingSymlinkWithoutCreatingTarget(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "private", "state.lock")
	target := filepath.Join(root, "outside-target")
	var symlinkErr error
	file, err := openPrivateFileWithHooks(path, true, true, privateFileOpenHooks{
		afterInspect: func(missing bool) error {
			if !missing {
				return errors.New("lock unexpectedly existed before symlink race")
			}
			symlinkErr = os.Symlink(target, path)
			return symlinkErr
		},
	})
	if file != nil {
		_ = file.Close()
	}
	if symlinkErr != nil {
		t.Skipf("symlink creation is unavailable: %v", symlinkErr)
	}
	if err == nil {
		t.Fatal("dangling lock symlink won the create-only open")
	}
	if _, err := os.Lstat(target); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("rejected dangling symlink created its target: %v", err)
	}
}

func TestOpenLockCreationRaceRejectsHardLinkWithoutModifyingTarget(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "private", "state.lock")
	victim := filepath.Join(root, "victim")
	want := []byte("victim-content")
	if err := os.WriteFile(victim, want, 0o644); err != nil {
		t.Fatal(err)
	}
	before, err := os.Stat(victim)
	if err != nil {
		t.Fatal(err)
	}
	file, err := openPrivateFileWithHooks(path, true, true, privateFileOpenHooks{
		afterInspect: func(missing bool) error {
			if !missing {
				return errors.New("lock unexpectedly existed before hard-link race")
			}
			return os.Link(victim, path)
		},
	})
	if file != nil {
		_ = file.Close()
	}
	if err == nil {
		t.Fatal("hard-linked creation winner was accepted as a private lock")
	}
	after, err := os.Stat(victim)
	if err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(victim)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(body, want) || before.Mode() != after.Mode() || before.Size() != after.Size() {
		t.Fatalf("rejected hard link modified victim: before=%v after=%v body=%q", before.Mode(), after.Mode(), body)
	}
}

func TestOpenLockExistingReplacementCannotRedirectOpen(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "private", "state.lock")
	if err := os.MkdirAll(filepath.Dir(path), PrivateDirectoryMode); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, nil, PrivateFileMode); err != nil {
		t.Fatal(err)
	}
	victim := filepath.Join(root, "victim")
	want := []byte("replacement-victim")
	if err := os.WriteFile(victim, want, 0o644); err != nil {
		t.Fatal(err)
	}
	file, err := openPrivateFileWithHooks(path, true, true, privateFileOpenHooks{
		afterInspect: func(missing bool) error {
			if missing {
				return errors.New("existing lock disappeared before replacement race")
			}
			if err := os.Rename(path, path+".original"); err != nil {
				return err
			}
			if err := os.Link(victim, path); err != nil {
				return fmt.Errorf("replace lock with hard link: %w", err)
			}
			return nil
		},
	})
	if file != nil {
		_ = file.Close()
	}
	if err == nil {
		t.Fatal("replacement lock identity was accepted")
	}
	body, err := os.ReadFile(victim)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(body, want) {
		t.Fatalf("replacement race modified victim: %q", body)
	}
}

func TestOpenLockParentReplacementCannotRedirectCreation(t *testing.T) {
	root := t.TempDir()
	directory := filepath.Join(root, "private")
	moved := filepath.Join(root, "private-moved")
	outside := filepath.Join(root, "outside")
	path := filepath.Join(directory, "state.lock")
	if err := os.Mkdir(outside, PrivateDirectoryMode); err != nil {
		t.Fatal(err)
	}
	var symlinkErr error
	file, err := openPrivateFileWithHooks(path, true, true, privateFileOpenHooks{
		afterInspect: func(missing bool) error {
			if !missing {
				return errors.New("lock unexpectedly existed before parent replacement")
			}
			if err := os.Rename(directory, moved); err != nil {
				return err
			}
			symlinkErr = os.Symlink(outside, directory)
			return symlinkErr
		},
	})
	if file != nil {
		_ = file.Close()
	}
	if symlinkErr != nil {
		t.Skipf("symlink creation is unavailable: %v", symlinkErr)
	}
	if err == nil {
		t.Fatal("replaced private parent was accepted")
	}
	for _, candidate := range []string{filepath.Join(outside, "state.lock"), filepath.Join(moved, "state.lock")} {
		if _, err := os.Lstat(candidate); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("rejected parent replacement left %s: %v", candidate, err)
		}
	}
}

func TestOpenLockRejectsInsecureParentReplacementBeforeDescriptorBinding(t *testing.T) {
	root := t.TempDir()
	directory := filepath.Join(root, "private")
	moved := filepath.Join(root, "private-moved")
	path := filepath.Join(directory, "state.lock")
	if err := os.Mkdir(directory, PrivateDirectoryMode); err != nil {
		t.Fatal(err)
	}
	file, err := openPrivateFileWithHooks(path, true, true, privateFileOpenHooks{
		afterInspect: func(missing bool) error {
			if !missing {
				return errors.New("lock unexpectedly existed before parent replacement")
			}
			if err := os.Rename(directory, moved); err != nil {
				return err
			}
			return os.Mkdir(directory, 0o755)
		},
	})
	if file != nil {
		_ = file.Close()
	}
	if err == nil {
		t.Fatal("insecure parent replacement was accepted before descriptor binding")
	}
	for _, candidate := range []string{filepath.Join(directory, "state.lock"), filepath.Join(moved, "state.lock")} {
		if _, statErr := os.Lstat(candidate); !errors.Is(statErr, os.ErrNotExist) {
			t.Fatalf("rejected parent replacement left %s: %v", candidate, statErr)
		}
	}
}

func TestOpenLockRejectsParentReplacementAfterDescriptorBinding(t *testing.T) {
	root := t.TempDir()
	directory := filepath.Join(root, "private")
	moved := filepath.Join(root, "private-moved")
	path := filepath.Join(directory, "state.lock")
	if err := os.Mkdir(directory, PrivateDirectoryMode); err != nil {
		t.Fatal(err)
	}
	file, err := openPrivateFileWithHooks(path, true, true, privateFileOpenHooks{
		afterParentOpen: func() error {
			if err := os.Rename(directory, moved); err != nil {
				return err
			}
			return os.Mkdir(directory, 0o755)
		},
	})
	if file != nil {
		_ = file.Close()
	}
	if err == nil {
		t.Fatal("parent replacement after descriptor binding was accepted")
	}
	for _, candidate := range []string{filepath.Join(directory, "state.lock"), filepath.Join(moved, "state.lock")} {
		if _, statErr := os.Lstat(candidate); !errors.Is(statErr, os.ErrNotExist) {
			t.Fatalf("rejected parent replacement left %s: %v", candidate, statErr)
		}
	}
}
