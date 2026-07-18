package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"reconc.dev/reconc/buildprovenance"
)

func TestRunPrintsDeterministicBuildMarker(t *testing.T) {
	root := t.TempDir()
	writeCommandFile(t, root, "go.mod", "module example.test/reconc\n\ngo 1.23\n")
	writeCommandFile(t, root, "cmd/reconc/main.go", "package main\n\nfunc main() {}\n")

	var stdout bytes.Buffer
	if err := run([]string{"--root", root, "--goos", "darwin", "--goarch", "arm64", "--version", "0.8.5"}, &stdout); err != nil {
		t.Fatalf("run: %v", err)
	}
	marker := strings.TrimSpace(stdout.String())
	provenance, err := buildprovenance.ParseMarker(marker)
	if err != nil {
		t.Fatalf("parse marker: %v", err)
	}
	if provenance.Version != "0.8.5" || provenance.GOOS != "darwin" || provenance.GOARCH != "arm64" || len(provenance.SourceDigest) != 64 {
		t.Fatalf("unexpected provenance: %#v", provenance)
	}
}

func TestRunRequiresCompleteTarget(t *testing.T) {
	var stdout bytes.Buffer
	if err := run([]string{"--root", t.TempDir(), "--goos", "darwin", "--version", "0.8.5"}, &stdout); err == nil {
		t.Fatal("expected missing GOARCH to fail")
	}
}

func TestRunVerifiesBinaryWithoutExecutingIt(t *testing.T) {
	root := t.TempDir()
	writeCommandFile(t, root, "go.mod", "module example.test/reconc\n\ngo 1.23\n")
	writeCommandFile(t, root, "cmd/reconc/main.go", "package main\n\nfunc main() {}\n")
	var marker bytes.Buffer
	args := []string{"--root", root, "--goos", "darwin", "--goarch", "arm64", "--version", "0.8.5"}
	if err := run(args, &marker); err != nil {
		t.Fatalf("create marker: %v", err)
	}
	binary := filepath.Join(t.TempDir(), "reconc")
	if err := os.WriteFile(binary, []byte("not executable\x00"+strings.TrimSpace(marker.String())+"\x00"), 0o600); err != nil {
		t.Fatal(err)
	}
	var stdout bytes.Buffer
	if err := run(append(args, "--verify-binary", binary), &stdout); err != nil {
		t.Fatalf("verify binary: %v", err)
	}
	if stdout.Len() != 0 {
		t.Fatalf("verification wrote output: %q", stdout.String())
	}
	if err := os.WriteFile(binary, []byte("not executable\x00"+strings.Replace(marker.String(), "version=0.8.5", "version=0.8.4", 1)+"\x00"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := run(append(args, "--verify-binary", binary), &stdout); err == nil {
		t.Fatal("mismatched binary provenance passed")
	}
}

func writeCommandFile(t *testing.T, root string, relative string, content string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(relative))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
