//go:build !windows

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestWorkflowAuditRunnerRejectsSymlinkedCacheDirectory(t *testing.T) {
	root := t.TempDir()
	target := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".reconc"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(root, ".reconc", "cache")); err != nil {
		t.Fatal(err)
	}
	runner, err := filepath.Abs("run-workflow-audit")
	if err != nil {
		t.Fatal(err)
	}
	command := exec.Command("/bin/sh", runner, "all")
	command.Dir = root
	output, err := command.CombinedOutput()
	if err == nil || !strings.Contains(string(output), "must not be a symlink") {
		t.Fatalf("symlinked cache directory was not rejected: err=%v output=%s", err, output)
	}
	entries, err := os.ReadDir(target)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("symlink target received runner state: %v", entries)
	}
}
