package agentsession

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// countGitIndexListings routes Git through a wrapper that counts
// `git ls-files` processes.
func countGitIndexListings(t *testing.T) func() int {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the counting Git wrapper is a POSIX shell script")
	}
	gitPath, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	logPath := filepath.Join(t.TempDir(), "ls-files.log")
	wrapperDir := t.TempDir()
	script := "#!/bin/sh\nfor arg in \"$@\"; do\n  if [ \"$arg\" = ls-files ]; then\n    printf '%s\\n' \"$*\" >> '" + logPath + "'\n    break\n  fi\ndone\nexec '" + gitPath + "' \"$@\"\n"
	if err := os.WriteFile(filepath.Join(wrapperDir, "git"), []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", wrapperDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return func() int {
		body, err := os.ReadFile(logPath)
		if os.IsNotExist(err) {
			return 0
		}
		if err != nil {
			t.Fatal(err)
		}
		return strings.Count(string(body), "\n")
	}
}

// One attempt lists the index for the same dirty paths once while the index
// keeps its bytes; a changed index is listed again.
func TestStopIndexListingIsReusedOnlyForAnUnchangedIndex(t *testing.T) {
	repo := t.TempDir()
	runGitGuardTestCommand(t, "-C", repo, "init", "--quiet")
	runGitGuardTestCommand(t, "-C", repo, "config", "user.email", "test@example.invalid")
	runGitGuardTestCommand(t, "-C", repo, "config", "user.name", "test")
	tracked := filepath.Join(repo, "tracked.txt")
	if err := os.WriteFile(tracked, []byte("one\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGitGuardTestCommand(t, "-C", repo, "add", "tracked.txt")
	runGitGuardTestCommand(t, "-C", repo, "commit", "--quiet", "-m", "init")
	if err := os.WriteFile(tracked, []byte("two\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	listings := countGitIndexListings(t)
	status := func() string {
		raw, err := gitCommandOutput(repo, "status", "--porcelain=v1", "-z", "--ignore-submodules=none", "--untracked-files=all")
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}
	scan := &stopPolicyScanCache{}
	first := gitDirtyFilesWithScan(repo, status(), scan)
	second := gitDirtyFilesWithScan(repo, status(), scan)
	if len(first) != 1 || first[0].IndexEntry == "" || len(second) != 1 || second[0] != first[0] || listings() != 1 {
		t.Fatalf("unchanged index: first %+v second %+v after %d listings; want one listing reused", first, second, listings())
	}
	runGitGuardTestCommand(t, "-C", repo, "add", "tracked.txt")
	staged := gitDirtyFilesWithScan(repo, status(), scan)
	if len(staged) != 1 || staged[0].IndexEntry == first[0].IndexEntry || strings.HasPrefix(staged[0].IndexEntry, "error:") || listings() != 2 {
		t.Fatalf("changed index: %+v after %d listings; want a new listing with the staged entry", staged, listings())
	}
}

// A listing taken while another index was swapped in and the old bytes were
// then restored is never kept: equal bytes before and after do not prove Git
// listed them. An in-place restore changes the change time, a renamed one the
// inode.
func TestStopIndexListingRejectsAnIndexRestoredDuringTheListing(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the index-swapping Git wrapper is a POSIX shell script")
	}
	for _, restore := range []string{"in-place", "rename"} {
		t.Run(restore, func(t *testing.T) {
			repo := t.TempDir()
			gitInitHelper(t, repo)
			index := filepath.Join(repo, ".git", "index")
			original, err := os.ReadFile(index)
			if err != nil {
				t.Fatal(err)
			}
			paths := []string{"initial"}
			if err := os.WriteFile(filepath.Join(repo, "initial"), []byte("staged\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			runGitGuardTestCommand(t, "-C", repo, "add", "initial")
			staged, err := os.ReadFile(index)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(index, original, 0o644); err != nil {
				t.Fatal(err)
			}
			gitPath, err := exec.LookPath("git")
			if err != nil {
				t.Fatal(err)
			}
			shim := t.TempDir()
			originalCopy, stagedCopy := filepath.Join(shim, "original"), filepath.Join(shim, "staged")
			if err := os.WriteFile(originalCopy, original, 0o644); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(stagedCopy, staged, 0o644); err != nil {
				t.Fatal(err)
			}
			install := func(source string) string {
				if restore == "in-place" {
					return "/bin/cp '" + source + "' '" + index + "'\n"
				}
				return "/bin/cp '" + source + "' '" + index + ".swap' && /bin/mv '" + index + ".swap' '" + index + "'\n"
			}
			script := "#!/bin/sh\nfor arg in \"$@\"; do\n  if [ \"$arg\" = ls-files ]; then\n" +
				install(stagedCopy) + "    '" + gitPath + "' \"$@\"\n    status=$?\n" + install(originalCopy) +
				"    exit \"$status\"\n  fi\ndone\nexec '" + gitPath + "' \"$@\"\n"
			if err := os.WriteFile(filepath.Join(shim, "git"), []byte(script), 0o700); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", shim+string(os.PathListSeparator)+os.Getenv("PATH"))
			scan := &stopPolicyScanCache{}
			gitIndexEntriesWithScan(repo, paths, scan)
			if restored, err := os.ReadFile(index); err != nil || !bytes.Equal(restored, original) {
				t.Fatalf("the wrapper did not restore the original index bytes: %v", err)
			}
			if scan.indexListing != nil {
				t.Fatalf("kept a listing of an index swapped during the listing: %+v", scan.indexListing.entries)
			}
		})
	}
}

// A Stop that evaluates a dirty tracked file lists the index once: the
// post-evaluation fingerprint reuses the listing of the unchanged index.
func TestEvaluatedStopListsTheIndexOnceWhileItIsUnchanged(t *testing.T) {
	repo := setupPolicyRepo(t)
	gitInitHelper(t, repo)
	if _, err := InitializeSessionState(repo, "index-once"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "initial"), []byte("changed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	listings := countGitIndexListings(t)
	if result := RunStop(repo, []byte(`{"session_id":"index-once"}`)); result.ExitCode != 0 || result.Stdout != "" {
		t.Fatalf("Stop = %+v", result)
	}
	state, err := LoadSessionState(repo, "index-once")
	if err != nil {
		t.Fatal(err)
	}
	if state.StopPolicyFingerprint == "" {
		t.Fatal("the evaluated Stop published no cache fingerprint, so its post-evaluation check did not run")
	}
	if got := listings(); got != 1 {
		t.Fatalf("evaluated Stop ran %d index listings; want 1", got)
	}
}
