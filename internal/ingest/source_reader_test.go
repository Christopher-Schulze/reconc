package ingest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRepositorySourceReaderRejectsTraversal(t *testing.T) {
	parent := t.TempDir()
	repo := filepath.Join(parent, "repo")
	if err := os.Mkdir(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	reader, err := newRepositorySourceReader(repo)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	if _, err := reader.Read("../outside.yml"); err == nil || !strings.Contains(err.Error(), "outside the repository root") {
		t.Fatalf("traversal error = %v", err)
	}
}

func TestRepositorySourceReaderRemainsAnchoredOrBlocksRootReplacement(t *testing.T) {
	parent := t.TempDir()
	repo := filepath.Join(parent, "repo")
	original := filepath.Join(parent, "original")
	if err := os.Mkdir(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, repo, "policy.yml", "original\n")
	reader, err := newRepositorySourceReader(repo)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	renameErr := os.Rename(repo, original)
	if renameErr != nil {
		body, readErr := reader.Read("policy.yml")
		if readErr != nil || string(body) != "original\n" {
			t.Fatalf("blocked root replacement disturbed anchored read: body=%q err=%v rename=%v", body, readErr, renameErr)
		}
		if _, statErr := os.Lstat(original); !os.IsNotExist(statErr) {
			t.Fatalf("blocked root replacement created destination: %v", statErr)
		}
		return
	}
	if err := os.Mkdir(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, repo, "policy.yml", "replacement\n")
	body, err := reader.Read("policy.yml")
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "original\n" {
		t.Fatalf("rooted read observed replacement bytes %q", body)
	}
}

func TestRepositorySourceReaderRejectsUseAfterClose(t *testing.T) {
	reader, err := newRepositorySourceReader(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := reader.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := reader.Read("policy.yml"); err == nil {
		t.Fatal("closed repository source reader remained usable")
	}
}
