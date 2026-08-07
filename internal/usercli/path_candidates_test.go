package usercli

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestExecutableCandidateNamesFollowsPATHEXT pins the names a bare `reconc`
// can resolve to. On Windows a shell walks PATHEXT, so a reconc.bat earlier in
// PATH runs instead of the installed reconc.exe; scanning only the .exe
// reported that installation as unshadowed.
func TestExecutableCandidateNamesFollowsPATHEXT(t *testing.T) {
	if runtime.GOOS != "windows" {
		if got := executableCandidateNames(); len(got) != 1 || got[0] != "reconc" {
			t.Fatalf("POSIX candidate names = %v, want [reconc]", got)
		}
		return
	}
	t.Setenv("PATHEXT", ".COM;.EXE;.BAT;.CMD")
	got := executableCandidateNames()
	want := []string{"reconc.com", "reconc.exe", "reconc.bat", "reconc.cmd"}
	if len(got) != len(want) {
		t.Fatalf("candidate names = %v, want %v", got, want)
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("candidate names = %v, want %v (resolution order matters)", got, want)
		}
	}
	t.Setenv("PATHEXT", "")
	if fallback := executableCandidateNames(); len(fallback) == 0 || fallback[0] != "reconc.com" {
		t.Fatalf("unset PATHEXT fallback = %v", fallback)
	}
	t.Setenv("PATHEXT", ";;garbage;.EXE;.exe")
	deduped := executableCandidateNames()
	if len(deduped) != 1 || deduped[0] != "reconc.exe" {
		t.Fatalf("malformed PATHEXT = %v, want a single reconc.exe", deduped)
	}
}

// TestPathCandidatesReportsShadowsInResolutionOrder proves the scan keeps the
// order a shell resolves in, so the first candidate is the binary that would
// actually run.
func TestPathCandidatesReportsShadowsInResolutionOrder(t *testing.T) {
	first := t.TempDir()
	second := t.TempDir()
	name := "reconc"
	if runtime.GOOS == "windows" {
		name = "reconc.exe"
		t.Setenv("PATHEXT", ".EXE")
	}
	for _, directory := range []string{first, second} {
		if err := os.WriteFile(filepath.Join(directory, name), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", strings.Join([]string{first, second}, string(os.PathListSeparator)))

	candidates, err := pathCandidates()
	if err != nil {
		t.Fatalf("pathCandidates: %v", err)
	}
	if len(candidates) != 2 {
		t.Fatalf("candidates = %v, want both PATH entries", candidates)
	}
	if !strings.HasPrefix(candidates[0], mustResolve(t, first)) {
		t.Fatalf("candidates = %v, want the earlier PATH entry first", candidates)
	}
}

func mustResolve(t *testing.T, path string) string {
	t.Helper()
	resolved, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	_ = resolved
	return canonicalPathPrefix(path)
}

func canonicalPathPrefix(path string) string {
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return path
	}
	return resolved
}
