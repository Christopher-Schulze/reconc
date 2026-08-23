package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"reconc.dev/reconc/internal/usercli"
)

func TestRunWritesDeterministicStrictManifest(t *testing.T) {
	directory := t.TempDir()
	if err := os.WriteFile(filepath.Join(directory, "z"), []byte("z"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "a"), []byte("alpha"), 0o600); err != nil {
		t.Fatal(err)
	}
	var stdout bytes.Buffer
	args := []string{"--output-dir", directory, "--version", "1.2.3"}
	if err := run(args, &stdout); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(directory, manifestName)
	first, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	stdout.Reset()
	if err := run(args, &stdout); err != nil {
		t.Fatal(err)
	}
	second, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, second) || !strings.Contains(stdout.String(), "unchanged") {
		t.Fatalf("manifest is not idempotent\nfirst=%s\nsecond=%s\nstdout=%s", first, second, stdout.String())
	}
	stdout.Reset()
	if err := run(append(args, "--verify"), &stdout); err != nil ||
		!strings.Contains(stdout.String(), "verified") {
		t.Fatalf("verify: %v stdout=%s", err, stdout.String())
	}
	if err := os.WriteFile(filepath.Join(directory, "a"), []byte("changed"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := run(append(args, "--verify"), &bytes.Buffer{}); err == nil {
		t.Fatal("stale manifest passed verification")
	}
	var manifest usercli.ReleaseManifest
	if err := json.Unmarshal(first, &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.Repository != repository || manifest.Tag != "reconc-v1.2.3" ||
		len(manifest.Assets) != 2 || manifest.Assets[0].Name != "a" || manifest.Assets[1].Name != "z" {
		t.Fatalf("manifest = %+v", manifest)
	}
}

func TestRunRejectsInvalidVersionIrregularAndEmptyInventory(t *testing.T) {
	if err := run([]string{"--output-dir", t.TempDir(), "--version", "v1"}, &bytes.Buffer{}); err == nil {
		t.Fatal("invalid version passed")
	}
	if err := run([]string{"--output-dir", t.TempDir(), "--version", "1.2.3"}, &bytes.Buffer{}); err == nil {
		t.Fatal("empty inventory passed")
	}
	invalidNameDirectory := t.TempDir()
	if err := os.WriteFile(filepath.Join(invalidNameDirectory, "_completion"), []byte("unsafe"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := run([]string{"--output-dir", invalidNameDirectory, "--version", "1.2.3"}, &bytes.Buffer{}); err == nil ||
		!strings.Contains(err.Error(), "invalid release asset") {
		t.Fatalf("consumer-invalid release name error = %v", err)
	}
	if runtime.GOOS == "windows" {
		return
	}
	directory := t.TempDir()
	if err := os.Symlink(t.TempDir(), filepath.Join(directory, "linked")); err != nil {
		t.Fatal(err)
	}
	if err := run([]string{"--output-dir", directory, "--version", "1.2.3"}, &bytes.Buffer{}); err == nil {
		t.Fatal("symlink inventory passed")
	}
}
