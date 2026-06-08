package agentsession

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestDirtyPathsFromStatusIncludesRenamePairAndSkipsRuntimeState(t *testing.T) {
	status := " M src/a.go\x00R  src/new.go\x00src/old.go\x00?? docs/new.md\x00 M .reconc/cache/report.json\x00?? .reconc/reports/s1.json\x00?? .reconc/locks/s1.stop-policy.lock\x00"
	got := dirtyPathsFromStatus(status)
	want := []string{"docs/new.md", "src/a.go", "src/new.go", "src/old.go"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("dirty paths mismatch\ngot:  %#v\nwant: %#v", got, want)
	}
}

func TestStopPolicyFingerprintTracksDirtyContentWithoutFullDiff(t *testing.T) {
	counterPath := filepath.Join(t.TempDir(), "counter")
	repo := setupStopScriptPolicyRepo(t, counterPath, 0, "")
	state, err := InitializeSessionState(repo, "s-dirty-fingerprint")
	if err != nil {
		t.Fatal(err)
	}
	srcDir := filepath.Join(repo, "src")
	if err := os.MkdirAll(srcDir, 0o755); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(srcDir, "a.go")

	if err := os.WriteFile(target, []byte("one\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("git", "-C", repo, "add", "src/a.go")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git add: %v\n%s", err, out)
	}
	cmd = exec.Command("git", "-C", repo, "commit", "-m", "track a", "--quiet")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git commit: %v\n%s", err, out)
	}

	if err := os.WriteFile(target, []byte("two\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	first := stopPolicyFingerprint(repo, state)

	if err := os.WriteFile(target, []byte("three\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	second := stopPolicyFingerprint(repo, state)
	if first == second {
		t.Fatal("fingerprint must change when tracked worktree content changes")
	}

	cmd = exec.Command("git", "-C", repo, "add", "src/a.go")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git add: %v\n%s", err, out)
	}
	staged := stopPolicyFingerprint(repo, state)
	if staged == second {
		t.Fatal("fingerprint must change when dirty content moves into the index")
	}

	if err := os.WriteFile(target, []byte("four\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	unstagedAfterStaged := stopPolicyFingerprint(repo, state)
	if unstagedAfterStaged == staged {
		t.Fatal("fingerprint must change when worktree content diverges from the staged blob")
	}
}

func TestStopPolicyFingerprintUntrackedModeAllIsOptIn(t *testing.T) {
	counterPath := filepath.Join(t.TempDir(), "counter")
	repo := setupStopScriptPolicyRepo(t, counterPath, 0, "")
	state, err := InitializeSessionState(repo, "s-untracked-mode")
	if err != nil {
		t.Fatal(err)
	}
	srcDir := filepath.Join(repo, "scratch")
	if err := os.MkdirAll(srcDir, 0o755); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(srcDir, "a.txt")
	if err := os.WriteFile(target, []byte("one\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	t.Setenv(stopPolicyUntrackedModeEnv, "")
	normalA := stopPolicyFingerprint(repo, state)
	if err := os.WriteFile(target, []byte("two\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	normalB := stopPolicyFingerprint(repo, state)
	if normalA != normalB {
		t.Fatal("default normal untracked mode should not hash every file inside an untracked directory")
	}

	t.Setenv(stopPolicyUntrackedModeEnv, "all")
	allA := stopPolicyFingerprint(repo, state)
	if err := os.WriteFile(target, []byte("three\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	allB := stopPolicyFingerprint(repo, state)
	if allA == allB {
		t.Fatal("all untracked mode must fingerprint file content inside untracked directories")
	}
}

func TestStopPolicyGitStatusNormalTracksUntrackedDirectorySentinel(t *testing.T) {
	counterPath := filepath.Join(t.TempDir(), "counter")
	repo := setupStopScriptPolicyRepo(t, counterPath, 0, "")
	if err := os.MkdirAll(filepath.Join(repo, "scratch"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "scratch", "a.txt"), []byte("one\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	t.Setenv(stopPolicyUntrackedModeEnv, "")
	status, mode := stopPolicyGitStatus(repo)
	if mode != "normal" {
		t.Fatalf("expected normal mode, got %q", mode)
	}
	if !strings.Contains(status, "?? scratch/\x00") {
		t.Fatalf("normal status should include untracked directory sentinel, got %q", status)
	}
}
