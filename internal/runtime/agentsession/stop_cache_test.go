package agentsession

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"reconc.dev/reconc/internal/compiler"
)

func TestDirtyPathsFromStatusIncludesRenamePairAndSkipsRuntimeState(t *testing.T) {
	status := " M src/a.go\x00R  src/new.go\x00src/old.go\x00?? docs/new.md\x00 M .reconc/cache/report.json\x00?? .reconc/reports/s1.json\x00?? .reconc/locks/s1.stop-policy.lock\x00"
	got := dirtyPathsFromStatus(status)
	want := []string{"docs/new.md", "src/a.go", "src/new.go", "src/old.go"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("dirty paths mismatch\ngot:  %#v\nwant: %#v", got, want)
	}
}

func TestDirtyPathsFromStatusKeepsUserPathsContainingRuntimeMarker(t *testing.T) {
	// A user file whose name merely contains a runtime marker substring
	// (for example "x.reconc/run/") is not Reconc-owned state and must
	// stay in the fingerprint; only paths rooted at ".reconc/" are
	// filtered.
	status := " M src/x.reconc/run/data.txt\x00 M .reconc/run/decisions.jsonl\x00"
	got := dirtyPathsFromStatus(status)
	want := []string{"src/x.reconc/run/data.txt"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("dirty paths mismatch\ngot:  %#v\nwant: %#v", got, want)
	}
}

func TestDirtyPathsFromStatusKeepsVerbatimPathBytes(t *testing.T) {
	// A rename origin whose third byte is a space must not lose its
	// first three characters, and leading/trailing spaces are part of
	// the filename with -z output.
	status := "R  dst.go\x00ab cd.go\x00 M  spaced name .go\x00?? für/é.go\x00"
	got := dirtyPathsFromStatus(status)
	want := []string{" spaced name .go", "ab cd.go", "dst.go", "für/é.go"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("dirty paths mismatch\ngot:  %#v\nwant: %#v", got, want)
	}
}

func TestHashFileContentBoundsOversizedFiles(t *testing.T) {
	dir := t.TempDir()

	// Bounded files keep the exact content hash.
	bounded := filepath.Join(dir, "bounded.txt")
	if err := os.WriteFile(bounded, []byte("fingerprint me\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := hashFileContent(bounded)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte("fingerprint me\n"))
	if want := hex.EncodeToString(sum[:]); got != want {
		t.Fatalf("bounded file hash = %s, want content hash %s", got, want)
	}

	// Oversized files receive a bounded diagnostic identity instead of a full read.
	oversized := filepath.Join(dir, "oversized.bin")
	file, err := os.Create(oversized)
	if err != nil {
		t.Fatal(err)
	}
	if err := file.Truncate(stopPolicyContentHashBound + 1); err != nil {
		file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	fixed := time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC)
	if err := os.Chtimes(oversized, fixed, fixed); err != nil {
		t.Fatal(err)
	}
	first, err := hashFileContent(oversized)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(first, "oversized:") {
		t.Fatalf("oversized fingerprint = %q, want oversized: prefix", first)
	}
	if repeated, err := hashFileContent(oversized); err != nil || repeated != first {
		t.Fatalf("oversized fingerprint not stable: %q vs %q (%v)", first, repeated, err)
	}
	// Growth changes the fingerprint so the stop cache invalidates.
	if err := os.Truncate(oversized, stopPolicyContentHashBound+2); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(oversized, fixed, fixed); err != nil {
		t.Fatal(err)
	}
	afterGrowth, err := hashFileContent(oversized)
	if err != nil {
		t.Fatal(err)
	}
	if afterGrowth == first {
		t.Fatalf("growth did not change the oversized fingerprint: %s", afterGrowth)
	}
	// An mtime-only change must invalidate as well.
	if err := os.Chtimes(oversized, fixed.Add(time.Hour), fixed.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	afterMtime, err := hashFileContent(oversized)
	if err != nil || afterMtime == afterGrowth {
		t.Fatalf("mtime change did not invalidate the oversized fingerprint: %q vs %q (%v)", afterGrowth, afterMtime, err)
	}
}

func TestOversizedDirtyFileDisablesStopPolicyCache(t *testing.T) {
	input := stopPolicyFingerprintInput{
		GitDirtyFiles: []gitDirtyFile{{Path: "large.bin", WorktreeHash: "oversized:67108865:2026-07-14T12:00:00Z"}},
	}
	if stopPolicyFingerprintCacheable(input) {
		t.Fatal("oversized dirty content must make the stop-policy cache ineligible")
	}
	input.GitDirtyFiles[0].WorktreeHash = strings.Repeat("a", 64)
	if !stopPolicyFingerprintCacheable(input) {
		t.Fatal("exact dirty content hash should remain cacheable")
	}
}

func TestOversizedDirtyFileForcesStopPolicyReevaluation(t *testing.T) {
	counterPath := filepath.Join(t.TempDir(), "counter")
	repo := setupStopScriptPolicyRepo(t, counterPath, 0, "")
	git := func(args ...string) {
		t.Helper()
		if out, err := exec.Command("git", append([]string{"-C", repo}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	git("add", "-A")
	git("commit", "-m", "fixture", "--quiet")
	target := filepath.Join(repo, "src", "a.go")
	if err := os.Truncate(target, stopPolicyContentHashBound+1); err != nil {
		t.Fatal(err)
	}
	if _, err := InitializeSessionState(repo, "s-oversized-cache"); err != nil {
		t.Fatal(err)
	}
	state, err := MutateSessionState(repo, "s-oversized-cache", func(state SessionState) SessionState {
		return AppendWritePath(state, "src/a.go")
	})
	if err != nil {
		t.Fatal(err)
	}
	for attempt := 1; attempt <= 2; attempt++ {
		if _, err := runStopPolicyCheck(repo, state); err != nil {
			t.Fatalf("stop policy attempt %d: %v", attempt, err)
		}
		state, err = LoadSessionState(repo, "s-oversized-cache")
		if err != nil {
			t.Fatal(err)
		}
	}
	if got := readCounter(t, counterPath); got != 2 {
		t.Fatalf("oversized dirty input reused a cached report: script runs=%d, want 2", got)
	}
	if state.StopPolicyFingerprint != "" || state.StopPolicyReportHash != "" {
		t.Fatalf("uncacheable stop state retained cache identity: %#v", state)
	}
}

func TestStopScopeWritePathsToUncommittedUsesFingerprintSnapshot(t *testing.T) {
	repo := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		if out, err := exec.Command("git", append([]string{"-C", repo}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	write := func(rel, body string) {
		t.Helper()
		full := filepath.Join(repo, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	git("init", "--quiet")
	git("config", "user.email", "t@t")
	git("config", "user.name", "t")
	write("clean.go", "package x\n")
	write("dirty.go", "package x\n")
	git("add", "clean.go", "dirty.go")
	git("commit", "-m", "base", "--quiet")
	write("dirty.go", "package x // dirty\n")
	write("newpkg/sub.go", "package newpkg\n")
	write("other-session.go", "package other\n")

	snapshot := stopPolicyGitSnapshotFor(repo)
	if !snapshot.StatusOK {
		t.Fatalf("status snapshot failed: %s", snapshot.Status)
	}
	abs := func(rel string) string { return filepath.Join(repo, rel) }
	writes := []string{abs("clean.go"), abs("dirty.go"), abs("newpkg/sub.go")}
	want := []string{abs("dirty.go"), abs("newpkg/sub.go")}
	if got := stopScopeWritePathsToUncommitted(repo, writes, snapshot); !reflect.DeepEqual(got, want) {
		t.Fatalf("scoped writes mismatch\ngot:  %#v\nwant: %#v", got, want)
	}

	git("add", "-A")
	git("commit", "-m", "finish", "--quiet")
	cleanSnapshot := stopPolicyGitSnapshotFor(repo)
	if got := stopScopeWritePathsToUncommitted(repo, writes, cleanSnapshot); len(got) != 0 {
		t.Fatalf("committed writes must leave no stop triggers, got %#v", got)
	}
}

func TestStopScopeWritePathsFailsClosed(t *testing.T) {
	repo := t.TempDir()
	writes := []string{filepath.Join(repo, "src", "a.go"), "../outside.md"}
	unknown := stopPolicyGitSnapshot{Status: "git failed", StatusMode: "normal", StatusOK: false}
	if got := stopScopeWritePathsToUncommitted(repo, writes, unknown); !reflect.DeepEqual(got, writes) {
		t.Fatalf("untrusted status must retain all writes\ngot:  %#v\nwant: %#v", got, writes)
	}
	dirty := stopPolicyGitSnapshot{Status: "?? src/\x00", StatusMode: "normal", StatusOK: true}
	if got := stopScopeWritePathsToUncommitted(repo, writes, dirty); !reflect.DeepEqual(got, writes) {
		t.Fatalf("unresolvable paths must be retained\ngot:  %#v\nwant: %#v", got, writes)
	}
}

func TestCachedCleanStopReportRequiresCurrentFingerprint(t *testing.T) {
	counterPath := filepath.Join(t.TempDir(), "counter")
	repo := setupStopScriptPolicyRepo(t, counterPath, 0, "")
	state, err := InitializeSessionState(repo, "s-exact-cache")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runStopPolicyCheck(repo, state); err != nil {
		t.Fatalf("populate cache: %v", err)
	}
	state, err = LoadSessionState(repo, "s-exact-cache")
	if err != nil {
		t.Fatal(err)
	}
	evidenceHash := stopPolicyEvidenceHash(state)
	if _, ok := cachedCleanStopPolicyReportForEvidence(state.RepoRoot, state, evidenceHash); !ok {
		t.Fatal("unchanged exact report should be reusable")
	}
	if err := os.WriteFile(filepath.Join(repo, "AGENTS.md"), []byte("# changed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, ok := cachedCleanStopPolicyReportForEvidence(state.RepoRoot, state, evidenceHash); ok {
		t.Fatal("dirty-state drift must invalidate the clean reentrant report")
	}
}

func TestStopPolicyEvidenceHashPreservesPathIdentity(t *testing.T) {
	plain := emptyState("/repo", "plain")
	plain.ReadPaths = []string{"file.go"}
	spaced := plain
	spaced.ReadPaths = []string{" file.go "}
	if stopPolicyEvidenceHash(plain) == stopPolicyEvidenceHash(spaced) {
		t.Fatal("space-distinct paths must not share a stop-policy evidence hash")
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

func TestCaptureCompletionStateBindsSessionIndexAndWorktree(t *testing.T) {
	counterPath := filepath.Join(t.TempDir(), "counter")
	repo := setupStopScriptPolicyRepo(t, counterPath, 0, "")
	git := func(args ...string) {
		t.Helper()
		if out, err := exec.Command("git", append([]string{"-C", repo}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	git("add", "-A")
	git("commit", "-m", "fixture", "--quiet")

	if _, err := InitializeSessionState(repo, "completion-a"); err != nil {
		t.Fatal(err)
	}
	first, err := CaptureCompletionState(repo)
	if err != nil {
		t.Fatal(err)
	}
	if !first.GitAvailable || !first.GitStatusOK || first.GitIndexHash == "" || len(first.DirtyPaths) != 0 {
		t.Fatalf("unexpected clean snapshot: %#v", first)
	}

	if _, err := InitializeSessionState(repo, "completion-b"); err != nil {
		t.Fatal(err)
	}
	second, err := CaptureCompletionState(repo)
	if err != nil {
		t.Fatal(err)
	}
	if second.Fingerprint == first.Fingerprint || second.SessionID != "completion-b" {
		t.Fatalf("session identity was not bound: first=%q second=%q", first.Fingerprint, second.Fingerprint)
	}

	if _, err := MutateSessionState(repo, "completion-b", func(state SessionState) SessionState {
		state.ReadPaths = append(state.ReadPaths, filepath.Join(repo, "src", "a.go"))
		state.EvidenceEpoch++
		return state
	}); err != nil {
		t.Fatal(err)
	}
	withEvidence, err := CaptureCompletionState(repo)
	if err != nil {
		t.Fatal(err)
	}
	if withEvidence.Fingerprint == second.Fingerprint || withEvidence.SessionEvidenceHash == second.SessionEvidenceHash {
		t.Fatal("active-session evidence did not change the completion candidate")
	}

	target := filepath.Join(repo, "src", "a.go")
	if err := os.WriteFile(target, []byte("package src // staged\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git("add", "src/a.go")
	staged, err := CaptureCompletionState(repo)
	if err != nil {
		t.Fatal(err)
	}
	if staged.Fingerprint == withEvidence.Fingerprint || staged.GitIndexHash == withEvidence.GitIndexHash || !staged.WorktreeMatchesIndex {
		t.Fatalf("staged index was not bound: %#v", staged)
	}

	if err := os.WriteFile(target, []byte("package src // unstaged\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	unstaged, err := CaptureCompletionState(repo)
	if err != nil {
		t.Fatal(err)
	}
	if unstaged.Fingerprint == staged.Fingerprint || unstaged.GitIndexHash != staged.GitIndexHash || unstaged.WorktreeMatchesIndex {
		t.Fatalf("worktree divergence was not bound: %#v", unstaged)
	}
}

func TestCaptureCompletionStateRejectsUnboundSessionReport(t *testing.T) {
	counterPath := filepath.Join(t.TempDir(), "counter")
	repo := setupStopScriptPolicyRepo(t, counterPath, 0, "")
	state, err := InitializeSessionState(repo, "completion-report")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runStopPolicyCheck(repo, state); err != nil {
		t.Fatal(err)
	}
	bound, err := CaptureCompletionState(repo)
	if err != nil {
		t.Fatal(err)
	}
	if !bound.SessionReportTrusted || bound.SessionReportHash == "" {
		t.Fatalf("fresh bound report was rejected: %#v", bound)
	}
	if _, err := MutateSessionState(repo, "completion-report", func(current SessionState) SessionState {
		current.StopPolicyReportHash = ""
		return current
	}); err != nil {
		t.Fatal(err)
	}
	unbound, err := CaptureCompletionState(repo)
	if err != nil {
		t.Fatal(err)
	}
	if unbound.SessionReportTrusted || unbound.SessionReportHash == "" {
		t.Fatalf("present report without a recorded hash was trusted: %#v", unbound)
	}
}

func TestCaptureCompletionStateBindsDirtySubmoduleContent(t *testing.T) {
	if testing.Short() {
		t.Skip("creates a local Git submodule fixture")
	}
	git := func(repo string, args ...string) {
		t.Helper()
		command := exec.Command("git", append([]string{"-C", repo}, args...)...)
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("git -C %s %v: %v\n%s", repo, args, err, output)
		}
	}
	child := t.TempDir()
	git(child, "init", "--quiet")
	git(child, "config", "user.name", "reconc-test")
	git(child, "config", "user.email", "reconc-test@example.com")
	if err := os.WriteFile(filepath.Join(child, "state.txt"), []byte("committed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(child, "add", "state.txt")
	git(child, "commit", "-m", "fixture", "--quiet")

	counterPath := filepath.Join(t.TempDir(), "counter")
	repo := setupStopScriptPolicyRepo(t, counterPath, 0, "")
	git(repo, "init", "--quiet")
	git(repo, "config", "user.name", "reconc-test")
	git(repo, "config", "user.email", "reconc-test@example.com")
	git(repo, "add", "-A")
	git(repo, "commit", "-m", "fixture", "--quiet")
	git(repo, "-c", "protocol.file.allow=always", "submodule", "add", "--quiet", child, "modules/child")
	git(repo, "commit", "-am", "add submodule", "--quiet")

	childCheckout := filepath.Join(repo, "modules", "child")
	if err := os.WriteFile(filepath.Join(childCheckout, "state.txt"), []byte("dirty-one\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	first, err := CaptureCompletionState(repo)
	if err != nil {
		t.Fatal(err)
	}
	if !first.WorktreeTrusted {
		t.Fatalf("readable dirty submodule was not trusted: %#v", first)
	}
	if err := os.WriteFile(filepath.Join(childCheckout, "state.txt"), []byte("dirty-two\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	second, err := CaptureCompletionState(repo)
	if err != nil {
		t.Fatal(err)
	}
	if second.Fingerprint == first.Fingerprint || second.WorktreeHash == first.WorktreeHash {
		t.Fatalf("dirty submodule content was not bound: first=%q second=%q", first.Fingerprint, second.Fingerprint)
	}
}

func TestCompletionDirtyFilesTrustFailsClosed(t *testing.T) {
	for _, file := range []gitDirtyFile{
		{Path: "fifo", WorktreeHash: "mode:prw-------"},
		{Path: "dir", WorktreeHash: "dir"},
		{Path: "unreadable", WorktreeHash: "error:permission denied"},
		{Path: "oversized", WorktreeHash: "oversized:67108865:2026-07-14T12:00:00Z"},
		{Path: "index", WorktreeHash: strings.Repeat("a", 64), IndexEntry: "error:index locked"},
		{Path: "submodule", WorktreeHash: "submodule-error:not a repository", IndexEntry: "160000 abc 0"},
		{Path: "link", WorktreeHash: "symlink:not-a-digest"},
		{Path: "submodule", WorktreeHash: "submodule:not-a-digest", IndexEntry: "160000 abc 0"},
	} {
		if completionDirtyFilesTrusted([]gitDirtyFile{file}) {
			t.Fatalf("unsafe dirty-file identity was trusted: %#v", file)
		}
	}
	for _, file := range []gitDirtyFile{
		{Path: "deleted", WorktreeHash: "missing"},
		{Path: "regular", WorktreeHash: strings.Repeat("a", 64)},
		{Path: "link", WorktreeHash: "symlink:" + strings.Repeat("b", 64)},
		{Path: "submodule", WorktreeHash: "submodule:" + strings.Repeat("c", 64), IndexEntry: "160000 abc 0"},
	} {
		if !completionDirtyFilesTrusted([]gitDirtyFile{file}) {
			t.Fatalf("safe dirty-file identity was rejected: %#v", file)
		}
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

	tracked := filepath.Join(repo, "tracked.txt")
	if err := os.WriteFile(tracked, []byte("v1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"add", "tracked.txt"}, {"commit", "-m", "track", "--quiet"}} {
		if out, err := exec.Command("git", append([]string{"-C", repo}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	if err := os.WriteFile(tracked, []byte("v2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	status, _ = stopPolicyGitStatus(repo)
	if !strings.Contains(status, "tracked.txt") || !strings.Contains(status, "?? scratch/\x00") {
		t.Fatalf("one status snapshot must contain tracked and untracked changes, got %q", status)
	}
}

func TestGitHeadFingerprintTracksCommitAndLinkedWorktree(t *testing.T) {
	repo := setupStopBenchmarkRepo(t)
	first := gitHeadFingerprint(repo)
	if strings.HasPrefix(first, "error:") || strings.HasSuffix(first, "missing") {
		t.Fatalf("initial HEAD fingerprint is invalid: %q", first)
	}
	if err := os.WriteFile(filepath.Join(repo, "src/a.go"), []byte("package src // next\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"add", "src/a.go"}, {"commit", "-m", "next", "--quiet"}} {
		if out, err := exec.Command("git", append([]string{"-C", repo}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	second := gitHeadFingerprint(repo)
	if second == first {
		t.Fatalf("HEAD fingerprint did not change after commit: %q", second)
	}
	worktree := filepath.Join(t.TempDir(), "linked")
	if out, err := exec.Command("git", "-C", repo, "worktree", "add", "--quiet", "-b", "linked-test", worktree, "HEAD").CombinedOutput(); err != nil {
		t.Fatalf("git worktree add: %v\n%s", err, out)
	}
	linked := gitHeadFingerprint(worktree)
	if strings.HasPrefix(linked, "error:") || strings.HasSuffix(linked, "missing") {
		t.Fatalf("linked-worktree HEAD fingerprint is invalid: %q", linked)
	}
}

func BenchmarkStopPolicyFingerprint(b *testing.B) {
	benchmarks := []struct {
		name  string
		dirty func(testing.TB, string)
	}{
		{name: "clean"},
		{
			name: "tracked-dirty",
			dirty: func(tb testing.TB, repo string) {
				tb.Helper()
				if err := os.WriteFile(filepath.Join(repo, "src/a.go"), []byte("package src // dirty\n"), 0o644); err != nil {
					tb.Fatal(err)
				}
			},
		},
		{
			name: "untracked-directory",
			dirty: func(tb testing.TB, repo string) {
				tb.Helper()
				path := filepath.Join(repo, "scratch/a.txt")
				if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
					tb.Fatal(err)
				}
				if err := os.WriteFile(path, []byte("scratch\n"), 0o644); err != nil {
					tb.Fatal(err)
				}
			},
		},
	}
	for _, benchmark := range benchmarks {
		b.Run(benchmark.name, func(b *testing.B) {
			repo := setupStopBenchmarkRepo(b)
			if benchmark.dirty != nil {
				benchmark.dirty(b, repo)
			}
			state, err := InitializeSessionState(repo, "fingerprint")
			if err != nil {
				b.Fatal(err)
			}
			state = AppendWritePath(state, "src/a.go")
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if got := stopPolicyFingerprint(repo, state); got == "" {
					b.Fatal("empty fingerprint")
				}
			}
		})
	}
}

func BenchmarkStopPolicyCheck(b *testing.B) {
	b.Run("cold-evidence", func(b *testing.B) {
		repo := setupStopBenchmarkRepo(b)
		state, err := InitializeSessionState(repo, "cold")
		if err != nil {
			b.Fatal(err)
		}
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			iteration := state
			iteration.Commands = []string{fmt.Sprintf("bench-%d", i)}
			if _, err := runStopPolicyCheck(repo, iteration); err != nil {
				b.Fatal(err)
			}
		}
	})

	b.Run("warm-exact", func(b *testing.B) {
		repo := setupStopBenchmarkRepo(b)
		state, err := InitializeSessionState(repo, "warm")
		if err != nil {
			b.Fatal(err)
		}
		if _, err := runStopPolicyCheck(repo, state); err != nil {
			b.Fatal(err)
		}
		state, err = LoadSessionState(repo, "warm")
		if err != nil {
			b.Fatal(err)
		}
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			if _, err := runStopPolicyCheck(repo, state); err != nil {
				b.Fatal(err)
			}
		}
	})

	b.Run("reentrant-clean", func(b *testing.B) {
		repo := setupStopBenchmarkRepo(b)
		state, err := InitializeSessionState(repo, "reentrant")
		if err != nil {
			b.Fatal(err)
		}
		if _, err := runStopPolicyCheck(repo, state); err != nil {
			b.Fatal(err)
		}
		state, err = LoadSessionState(repo, "reentrant")
		if err != nil {
			b.Fatal(err)
		}
		evidenceHash := stopPolicyEvidenceHash(state)
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			if _, ok := cachedCleanStopPolicyReportForEvidence(state.RepoRoot, state, evidenceHash); !ok {
				b.Fatal("exact reentrant report was not reusable")
			}
		}
	})
}

func setupStopBenchmarkRepo(tb testing.TB) string {
	tb.Helper()
	tb.Setenv("RECONC_HOME", tb.TempDir())
	tb.Setenv(StateRootEnv, tb.TempDir())
	repo := tb.TempDir()
	git := func(args ...string) {
		tb.Helper()
		if out, err := exec.Command("git", append([]string{"-C", repo}, args...)...).CombinedOutput(); err != nil {
			tb.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	write := func(rel, body string) {
		tb.Helper()
		path := filepath.Join(repo, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			tb.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			tb.Fatal(err)
		}
	}
	git("init", "--quiet")
	git("config", "user.email", "bench@reconc.dev")
	git("config", "user.name", "reconc benchmark")
	write("AGENTS.md", "# Benchmark\n")
	write("policies/rules.yml", "rules: []\n")
	write("src/a.go", "package src\n")
	if _, err := compiler.CompileRepoPolicy(repo, "benchmark"); err != nil {
		tb.Fatalf("compile policy: %v", err)
	}
	git("add", "-A")
	git("commit", "-m", "benchmark fixture", "--quiet")
	return repo
}
