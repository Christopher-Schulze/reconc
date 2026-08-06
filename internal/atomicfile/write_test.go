package atomicfile

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWriteIfChangedSkipsIdenticalPublication(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "state.json")
	written, err := WriteIfChanged(path, []byte("one\n"), 0o640)
	if err != nil || !written {
		t.Fatalf("first write: written=%v err=%v", written, err)
	}
	written, err = WriteIfChanged(path, []byte("one\n"), 0o640)
	if err != nil || written {
		t.Fatalf("identical write: written=%v err=%v", written, err)
	}
	written, err = WriteIfChanged(path, []byte("two\n"), 0o640)
	if err != nil || !written {
		t.Fatalf("changed write: written=%v err=%v", written, err)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "two\n" {
		t.Fatalf("published bytes: %q err=%v", data, err)
	}
	matches, err := filepath.Glob(filepath.Join(filepath.Dir(path), ".state.json.*.tmp"))
	if err != nil || len(matches) != 0 {
		t.Fatalf("atomic temp residue: %v err=%v", matches, err)
	}
}

func TestWriteIfChangedRejectsSymlinkTargets(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "target")
	link := filepath.Join(root, "state.json")
	if err := os.WriteFile(target, []byte("secret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if _, err := WriteIfChanged(link, []byte("replacement\n"), 0o600); err == nil {
		t.Fatal("symlink target must be rejected")
	}
	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "secret\n" {
		t.Fatalf("symlink target changed: %q", data)
	}
}

func TestWriteIfChangedReplacesDifferentSizedSparseFileWithoutWholeRead(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sparse")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := file.Truncate(64 << 20); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	written, err := WriteIfChanged(path, []byte("small\n"), 0o600)
	if err != nil || !written {
		t.Fatalf("replace sparse file: written=%v err=%v", written, err)
	}
	body, err := os.ReadFile(path)
	if err != nil || string(body) != "small\n" {
		t.Fatalf("replacement = %q, %v", body, err)
	}
}
