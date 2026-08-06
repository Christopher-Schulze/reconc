//go:build !windows

package runtime

import (
	"math"
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

// TestRunScriptRejectsIntermediateDirectorySymlinkEscape covers the escape the
// lexical parser and the leaf symlink check both miss: every path segment is a
// plain name and the script itself is a real executable file, but a directory
// on the way out of the repository is a symlink, so the process would run
// outside the repository the policy claims to bound it to.
func TestRunScriptRejectsIntermediateDirectorySymlinkEscape(t *testing.T) {
	repo := t.TempDir()
	outside := t.TempDir()
	marker := filepath.Join(outside, "ran")
	script := filepath.Join(outside, "check.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\ntouch \""+marker+"\"\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(repo, "scripts")); err != nil {
		t.Fatal(err)
	}
	outcome, err := RunScript(repo, "scripts/check.sh", nil, ScriptInput{}, 5, 1)
	if err == nil {
		t.Fatal("a script reached through an escaping directory symlink must be refused")
	}
	if outcome.Status != "error" {
		t.Fatalf("escaping script must fail closed with error status, got %q", outcome.Status)
	}
	if _, statErr := os.Stat(marker); statErr == nil {
		t.Fatal("refused script must never execute")
	}
}

// TestRunScriptAllowsRepositoryInternalDirectorySymlink keeps the containment
// check from degenerating into "reject every symlink": a directory symlink
// that stays inside the repository is a legitimate layout.
func TestRunScriptAllowsRepositoryInternalDirectorySymlink(t *testing.T) {
	repo := t.TempDir()
	real := filepath.Join(repo, "tools")
	if err := os.MkdirAll(real, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(real, "check.sh"), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(real, filepath.Join(repo, "scripts")); err != nil {
		t.Fatal(err)
	}
	outcome, err := RunScript(repo, "scripts/check.sh", nil, ScriptInput{}, 5, 1)
	if err != nil {
		t.Fatalf("repository-internal directory symlink must stay runnable: %v", err)
	}
	if outcome.Status != "pass" {
		t.Fatalf("expected pass outcome, got %q", outcome.Status)
	}
}

// TestRunScriptSurvivesOverflowingKillTimeout drives the shipped path with a
// kill_timeout_sec that overflows the raw conversion; without the clamp the
// wrapped negative WaitDelay disables the SIGKILL escalation.
func TestRunScriptSurvivesOverflowingKillTimeout(t *testing.T) {
	repo := t.TempDir()
	if err := os.WriteFile(filepath.Join(repo, "check.sh"), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	outcome, err := RunScript(repo, "check.sh", nil, ScriptInput{}, 5, math.MaxInt)
	if err != nil {
		t.Fatalf("overflowing kill timeout must not break a passing script: %v", err)
	}
	if outcome.Status != "pass" || outcome.ExitCode != 0 {
		t.Fatalf("expected pass outcome, got status=%q exit=%d", outcome.Status, outcome.ExitCode)
	}
}
