package main

import (
	"archive/zip"
	"bytes"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestHarnessPackGeneratorIsDeterministic(t *testing.T) {
	source := t.TempDir()
	if err := os.WriteFile(filepath.Join(source, "README.md"), []byte("fixture\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	manifest := filepath.Join(t.TempDir(), "manifest.json")
	archive := filepath.Join(t.TempDir(), "pack.zip")
	args := []string{"--write", "--source", source, "--manifest", manifest, "--archive", archive}
	var stdout bytes.Buffer
	if err := run(args, &stdout); err != nil {
		t.Fatal(err)
	}
	firstManifest, err := os.ReadFile(manifest)
	if err != nil {
		t.Fatal(err)
	}
	firstArchive, err := os.ReadFile(archive)
	if err != nil {
		t.Fatal(err)
	}
	if err := run([]string{"--check", "--source", source, "--manifest", manifest, "--archive", archive}, &stdout); err != nil {
		t.Fatal(err)
	}
	secondArchive, err := os.ReadFile(archive)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(firstArchive, secondArchive) || !strings.Contains(stdout.String(), "digest=") {
		t.Fatal("harness pack generation is not deterministic")
	}
	reader, err := zip.NewReader(bytes.NewReader(firstArchive), int64(len(firstArchive)))
	if err != nil {
		t.Fatal(err)
	}
	if len(reader.File) != 2 || reader.File[0].Name != "manifest.json" ||
		reader.File[1].Name != advancedPackPrefix+"/README.md" {
		t.Fatalf("archive inventory = %+v", reader.File)
	}
	if current, err := os.ReadFile(manifest); err != nil || !bytes.Equal(firstManifest, current) {
		t.Fatalf("manifest changed during check: %v", err)
	}
}

func TestHarnessPackCheckRejectsStaleManifest(t *testing.T) {
	source := t.TempDir()
	if err := os.WriteFile(filepath.Join(source, "README.md"), []byte("fixture\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	manifest := filepath.Join(t.TempDir(), "manifest.json")
	archive := filepath.Join(t.TempDir(), "pack.zip")
	if err := os.WriteFile(manifest, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(archive, []byte("stale"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := run([]string{
		"--check", "--source", source, "--manifest", manifest, "--archive", archive,
	}, &bytes.Buffer{}); err == nil {
		t.Fatal("stale harness pack manifest was accepted")
	}
}

func TestHarnessPackCheckRejectsStaleArchiveWithoutMutation(t *testing.T) {
	source := t.TempDir()
	if err := os.WriteFile(filepath.Join(source, "README.md"), []byte("fixture\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	manifest := filepath.Join(t.TempDir(), "manifest.json")
	archive := filepath.Join(t.TempDir(), "pack.zip")
	if err := run([]string{
		"--write", "--source", source, "--manifest", manifest, "--archive", archive,
	}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	stale := []byte("stale archive")
	if err := os.WriteFile(archive, stale, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := run([]string{
		"--check", "--source", source, "--manifest", manifest, "--archive", archive,
	}, &bytes.Buffer{}); err == nil {
		t.Fatal("stale harness pack archive was accepted")
	}
	current, err := os.ReadFile(archive)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(current, stale) {
		t.Fatal("read-only harness pack check mutated the stale archive")
	}
}

func TestCanonicalSourceIncludesOnlyTrackedBytesAndGitModes(t *testing.T) {
	repo := t.TempDir()
	runGit(t, repo, "init", "--quiet")
	source := filepath.Join(repo, "harness", "template")
	if err := os.MkdirAll(source, 0o755); err != nil {
		t.Fatal(err)
	}
	tracked := filepath.Join(source, "run")
	if err := os.WriteFile(tracked, []byte("#!/bin/sh\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "ignored.txt"), []byte("private\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, repo, "add", "harness/template/run")
	runGit(t, repo, "update-index", "--chmod=+x", "harness/template/run")

	sourceFS, err := canonicalSource(source)
	if err != nil {
		t.Fatal(err)
	}
	info, err := fs.Stat(sourceFS, "run")
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o755 {
		t.Fatalf("tracked Git mode = %04o, want 0755", info.Mode().Perm())
	}
	if _, err := fs.Stat(sourceFS, "ignored.txt"); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("ignored local file entered canonical source: %v", err)
	}
}

func runGit(t *testing.T, directory string, args ...string) {
	t.Helper()
	command := exec.Command("git", append([]string{"-C", directory}, args...)...)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, output)
	}
}
