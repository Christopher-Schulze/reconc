//go:build !windows

package runtime

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunScriptRejectsSymlink(t *testing.T) {
	repo := t.TempDir()
	target := filepath.Join(t.TempDir(), "outside.sh")
	if err := os.WriteFile(target, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(repo, "check.sh")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	_, err := RunScript(repo, "check.sh", nil, ScriptInput{}, 1, 1)
	if err == nil || !strings.Contains(err.Error(), "executable regular file") {
		t.Fatalf("expected symlink rejection, got %v", err)
	}
}
