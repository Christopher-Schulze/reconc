package boundedio

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestOptionalSnapshotDistinguishesInitialAbsence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "source.yml")
	called := false
	use := func(file *os.File, opened os.FileInfo) error {
		called = true
		if opened.Size() != 4 {
			t.Fatalf("opened size = %d", opened.Size())
		}
		body, err := io.ReadAll(file)
		if err != nil {
			return err
		}
		if string(body) != "rule" {
			t.Fatalf("opened content = %q", body)
		}
		return nil
	}
	exists, err := WithRegularFileSnapshotIfExists(path, 4, use)
	if err != nil || exists || called {
		t.Fatalf("initial absence = exists %t, callback %t, error %v", exists, called, err)
	}
	if err := WithRegularFileSnapshot(path, 4, use); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("strict snapshot lost missing-file error: %v", err)
	}
	if err := os.WriteFile(path, []byte("rule"), 0o600); err != nil {
		t.Fatal(err)
	}
	exists, err = WithRegularFileSnapshotIfExists(path, 4, use)
	if err != nil || !exists || !called {
		t.Fatalf("present snapshot = exists %t, callback %t, error %v", exists, called, err)
	}
}

func TestOptionalSnapshotDoesNotTreatExistingSymlinkAsAbsent(t *testing.T) {
	directory := t.TempDir()
	target := filepath.Join(directory, "target.yml")
	link := filepath.Join(directory, "source.yml")
	if err := os.WriteFile(target, []byte("rule"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	exists, err := WithRegularFileSnapshotIfExists(link, 4, func(*os.File, os.FileInfo) error {
		t.Fatal("symlink callback ran")
		return nil
	})
	if err == nil || !exists {
		t.Fatalf("existing symlink was accepted as absent: exists %t, error %v", exists, err)
	}
}
