package boundedio

import (
	"errors"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestReadFileEnforcesExactLimit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "input")
	if err := os.WriteFile(path, []byte("12345"), 0o600); err != nil {
		t.Fatal(err)
	}
	body, err := ReadFile(path, 5)
	if err != nil || string(body) != "12345" {
		t.Fatalf("exact-limit read = %q, %v", body, err)
	}
	if _, err := ReadFile(path, 4); err == nil || !strings.Contains(err.Error(), "exceeds 4 bytes") {
		t.Fatalf("oversized read error = %v", err)
	}
	if _, err := ReadFile(path, 0); err == nil {
		t.Fatal("non-positive limit must fail")
	}
}

func TestReadRegularFileRejectsOversizeSparseAndIrregularInputs(t *testing.T) {
	root := t.TempDir()
	exact := filepath.Join(root, "exact")
	if err := os.WriteFile(exact, []byte("12345"), 0o600); err != nil {
		t.Fatal(err)
	}
	if body, err := ReadRegularFile(exact, 5); err != nil || string(body) != "12345" {
		t.Fatalf("exact regular read = %q, %v", body, err)
	}
	sparse := filepath.Join(root, "sparse")
	file, err := os.Create(sparse)
	if err != nil {
		t.Fatal(err)
	}
	if err := file.Truncate(6); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadRegularFile(sparse, 5); err == nil || !strings.Contains(err.Error(), "exceeds 5 bytes") {
		t.Fatalf("sparse oversize error = %v", err)
	}
	if _, err := ReadRegularFile(root, 5); err == nil || !strings.Contains(err.Error(), "regular file") {
		t.Fatalf("directory error = %v", err)
	}
}

func TestReadRegularFileRejectsSymlinkAndFIFO(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink and FIFO fixture is Unix-specific")
	}
	root := t.TempDir()
	target := filepath.Join(root, "target")
	if err := os.WriteFile(target, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadRegularFile(link, 1); err == nil || !strings.Contains(err.Error(), "non-symlink") {
		t.Fatalf("symlink error = %v", err)
	}
	fifo := filepath.Join(root, "fifo")
	if err := syscallMkfifo(fifo); err != nil {
		t.Skipf("mkfifo unavailable: %v", err)
	}
	if _, err := ReadRegularFile(fifo, 1); err == nil || !strings.Contains(err.Error(), "regular file") {
		t.Fatalf("FIFO error = %v", err)
	}
	if _, err := ReadFile(fifo, 1); err == nil || !strings.Contains(err.Error(), "regular file") {
		t.Fatalf("symlink-following FIFO error = %v", err)
	}
}

func TestReadDirBoundsAndSymlinkPolicy(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"b", "a"} {
		if err := os.WriteFile(filepath.Join(root, name), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	entries, err := ReadDirNoSymlink(root, 2)
	if err != nil {
		t.Fatal(err)
	}
	if entries[0].Name() != "a" || entries[1].Name() != "b" {
		t.Fatalf("entries are not sorted: %v", entries)
	}
	if _, err := ReadDirNoSymlink(root, 1); err == nil || !strings.Contains(err.Error(), "directory entries") {
		t.Fatalf("entry limit error = %v", err)
	}
	link := filepath.Join(t.TempDir(), "directory-link")
	if err := os.Symlink(root, link); err != nil {
		if errors.Is(err, os.ErrPermission) || runtime.GOOS == "windows" {
			t.Skip("directory symlinks unavailable")
		}
		t.Fatal(err)
	}
	if _, err := ReadDirNoSymlink(link, 2); err == nil || !strings.Contains(err.Error(), "non-symlink directory") {
		t.Fatalf("strict directory symlink error = %v", err)
	}
	if entries, err := ReadDir(link, 2); err != nil || len(entries) != 2 {
		t.Fatalf("follow directory read = %d, %v", len(entries), err)
	}
}

func TestReadRejectsOverflowingLimits(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ok")
	if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadFile(path, math.MaxInt64); err == nil {
		t.Fatal("MaxInt64 byte limit must be rejected before LimitReader wrap")
	}
	if _, err := ReadDir(t.TempDir(), math.MaxInt); err == nil {
		t.Fatal("MaxInt entry limit must be rejected before ReadDir wrap")
	}
}
