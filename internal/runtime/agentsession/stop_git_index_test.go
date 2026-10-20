package agentsession

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// An untracked path has no index entry, so Stop asks Git's index only about
// the other dirty paths and skips the process when every dirty path is new.
func TestGitDirtyFilesListsIndexEntriesOnlyForTrackedPaths(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the counting Git wrapper is a POSIX shell script")
	}
	repo := t.TempDir()
	runGitGuardTestCommand(t, "-C", repo, "init", "--quiet")
	runGitGuardTestCommand(t, "-C", repo, "config", "user.email", "test@example.invalid")
	runGitGuardTestCommand(t, "-C", repo, "config", "user.name", "test")
	if err := os.WriteFile(filepath.Join(repo, "tracked.txt"), []byte("one\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGitGuardTestCommand(t, "-C", repo, "add", "tracked.txt")
	runGitGuardTestCommand(t, "-C", repo, "commit", "--quiet", "-m", "init")
	for _, name := range []string{"new-a.txt", "new-b.txt"} {
		if err := os.WriteFile(filepath.Join(repo, name), []byte(name+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
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
	status := func() string {
		raw, err := gitCommandOutput(repo, "status", "--porcelain=v1", "-z", "--ignore-submodules=none", "--untracked-files=all")
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}
	listings := func() []string {
		body, err := os.ReadFile(logPath)
		if os.IsNotExist(err) {
			return nil
		}
		if err != nil {
			t.Fatal(err)
		}
		return strings.Split(strings.TrimSpace(string(body)), "\n")
	}

	head, err := gitCommandOutput(repo, "rev-parse", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	snapshot := stopPolicyGitSnapshot{Head: strings.TrimSpace(head), Status: status(), StatusOK: true}
	captureStopRepositoryGenerationWithIdentityAndScan(repo, snapshot, "digest", 1, "task", nil, nil)
	if calls := listings(); calls != nil {
		t.Fatalf("repository generation listed untracked paths in the index: %q", calls)
	}
	files := gitDirtyFiles(repo, status())
	if len(files) != 2 || listings() != nil {
		t.Fatalf("untracked-only dirty files = %+v with ls-files calls %q; want two files and no index listing", files, listings())
	}
	for _, file := range files {
		if file.IndexEntry != "" || file.WorktreeHash == "" {
			t.Fatalf("untracked dirty file = %+v", file)
		}
	}

	if err := os.WriteFile(filepath.Join(repo, "tracked.txt"), []byte("two\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// A rename origin is a bare path field; one spelled like an untracked
	// record must still be listed.
	if indexed := indexedDirtyPaths([]string{"?? origin", "renamed"}, "R  renamed\x00?? origin\x00"); len(indexed) != 2 {
		t.Fatalf("rename origin was read as an untracked record: %q", indexed)
	}
	files = gitDirtyFiles(repo, status())
	calls := listings()
	if len(files) != 3 || len(calls) != 1 || !strings.HasSuffix(calls[0], "-- tracked.txt") {
		t.Fatalf("dirty files = %+v with ls-files calls %q; want one listing of tracked.txt only", files, calls)
	}
	for _, file := range files {
		if (file.Path == "tracked.txt") != (file.IndexEntry != "") {
			t.Fatalf("index entry of %s = %q", file.Path, file.IndexEntry)
		}
	}
}
