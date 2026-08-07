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

func TestHashFileContentExpectedRejectsFileReplacement(t *testing.T) {
	dir := t.TempDir()
	first := filepath.Join(dir, "first.txt")
	second := filepath.Join(dir, "second.txt")
	if err := os.WriteFile(first, []byte("first\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(second, []byte("second\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	expected, err := os.Lstat(first)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := hashFileContentExpected(second, expected); err == nil {
		t.Fatal("content hashing accepted a different file identity")
	}
}

func TestReadStopDirectoryEntriesRejectsBoundBeforeFullAllocation(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"a", "b"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(name), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	info, err := os.Lstat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := readStopDirectoryEntries(dir, info, 1); err == nil || !strings.Contains(err.Error(), "tree exceeds") {
		t.Fatalf("bounded directory read error = %v, want tree limit", err)
	}
}

func TestOversizedDirtyFileDisablesStopPolicyCache(t *testing.T) {
	input := stopPolicyFingerprintInput{
		GitStatusOK:   true,
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

func writePolicyLock(t *testing.T, repo, lock string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(repo, ".reconc"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, ".reconc", "policy.lock.json"), []byte(lock), 0o600); err != nil {
		t.Fatal(err)
	}
}

// TestStopPolicyCacheEligibilityAcrossLockShapes drives the shipped eligibility
// predicate across the lockfile shapes the compiler can emit. Only a policy
// this pass cannot bind statically leaves the warm path.
func TestStopPolicyCacheEligibilityAcrossLockShapes(t *testing.T) {
	cases := []struct {
		name      string
		lock      string
		cacheable bool
	}{
		{
			name:      "require_fresh_file is bound by expiry, not excluded",
			lock:      `{"rules":[{"id":"fresh","kind":"require_fresh_file","required_files":[{"path":"STATUS.md","max_age_hours":1}]}]}`,
			cacheable: true,
		},
		{
			name:      "require_evidence names a bindable path",
			lock:      `{"rules":[{"id":"ev","kind":"require_evidence","evidence":[{"file":"build/coverage.out","must_exist":true}]}]}`,
			cacheable: true,
		},
		{
			name:      "kind token quoted in a rule message",
			lock:      `{"rules":[{"id":"doc","kind":"require_claim","message":"use \"kind\":\"require_fresh_file\" for staleness gates","claims":["x"]}]}`,
			cacheable: true,
		},
		{
			name:      "template-generated evidence path cannot be bound",
			lock:      `{"rules":[{"id":"ev","kind":"require_evidence","evidence":[{"file":"reports/{capture}.json","must_exist":true}]}]}`,
			cacheable: false,
		},
		{
			name:      "undecodable lock stays out of the warm path",
			lock:      `{"rules":[`,
			cacheable: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			repo := t.TempDir()
			writePolicyLock(t, repo, tc.lock)
			input := stopPolicyFingerprintInput{RepoRoot: repo, GitStatusOK: true, WritePaths: []string{"src/a.go"}}
			if got := stopPolicyFingerprintCacheable(input); got != tc.cacheable {
				t.Fatalf("cacheable = %v, want %v", got, tc.cacheable)
			}
		})
	}
}

// TestStopPolicyFingerprintBindsGitignoredEvidence is the gap this pass closes:
// `git status` never lists ignored files, so a gitignored evidence file could
// be rewritten or deleted without moving any fingerprint field.
func TestStopPolicyFingerprintBindsGitignoredEvidence(t *testing.T) {
	repo := t.TempDir()
	writePolicyLock(t, repo, `{"rules":[{"id":"ev","kind":"require_evidence","evidence":[{"file":"build/coverage.out","must_exist":true}]}]}`)
	evidence := filepath.Join(repo, "build", "coverage.out")
	if err := os.MkdirAll(filepath.Dir(evidence), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(evidence, []byte("total: 91.4%\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	// The path is invisible to Git here: no repository, so no dirty entry can
	// carry it. Only the policy-input binding can notice the change.
	before := hashStopPolicyFingerprintInput(stopPolicyFingerprintInputFor(repo, SessionState{SessionID: "s1", WritePaths: []string{"src/a.go"}}))
	if err := os.WriteFile(evidence, []byte("total: 12.0%\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	after := hashStopPolicyFingerprintInput(stopPolicyFingerprintInputFor(repo, SessionState{SessionID: "s1", WritePaths: []string{"src/a.go"}}))
	if before == "" || before == after {
		t.Fatalf("rewriting a gitignored evidence file must move the fingerprint: %q", before)
	}

	if err := os.Remove(evidence); err != nil {
		t.Fatal(err)
	}
	removed := hashStopPolicyFingerprintInput(stopPolicyFingerprintInputFor(repo, SessionState{SessionID: "s1", WritePaths: []string{"src/a.go"}}))
	if removed == after {
		t.Fatal("deleting a gitignored evidence file must move the fingerprint")
	}
}

// TestStopPolicyReportExpiryFollowsFreshFileAge proves a stored report carries
// the instant its own inputs stop describing it, and that a report past that
// instant is no longer reused.
func TestStopPolicyReportExpiryFollowsFreshFileAge(t *testing.T) {
	repo := t.TempDir()
	writePolicyLock(t, repo, `{"rules":[{"id":"fresh","kind":"require_fresh_file","required_files":[{"path":"STATUS.md","max_age_hours":2}]}]}`)
	status := filepath.Join(repo, "STATUS.md")
	if err := os.WriteFile(status, []byte("current\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	modified := time.Now().Add(-30 * time.Minute)
	if err := os.Chtimes(status, modified, modified); err != nil {
		t.Fatal(err)
	}

	scan := scanStopPolicyLockfile(repo, nil)
	if len(scan.FreshFiles) != 1 || scan.FreshFiles[0].Path != "STATUS.md" {
		t.Fatalf("fresh-file requirement was not scanned: %+v", scan.FreshFiles)
	}
	expiry := stopPolicyReportExpiry(repo, scan.FreshFiles)
	want := modified.Add(2 * time.Hour).Unix()
	if expiry != want {
		t.Fatalf("expiry = %d, want %d", expiry, want)
	}
	if stopPolicyReportExpired(expiry) {
		t.Fatal("a report inside its age window must stay reusable")
	}
	if !stopPolicyReportExpired(time.Now().Add(-time.Second).Unix()) {
		t.Fatal("a report past its age window must be re-evaluated")
	}
	if stopPolicyReportExpired(0) {
		t.Fatal("a policy without wall-clock rules must never expire on time alone")
	}

	// A policy with no age requirement produces no expiry at all.
	plain := t.TempDir()
	writePolicyLock(t, plain, `{"rules":[{"id":"deny","kind":"deny_write","paths":["vendor/**"]}]}`)
	if got := stopPolicyReportExpiry(plain, scanStopPolicyLockfile(plain, nil).FreshFiles); got != 0 {
		t.Fatalf("expiry without an age requirement = %d, want 0", got)
	}
}

// TestMissingLockfileDisablesStopPolicyCache keeps the uncertainty branch
// fail-closed: a report we cannot revalidate is never reused.
func TestMissingLockfileDisablesStopPolicyCache(t *testing.T) {
	input := stopPolicyFingerprintInput{RepoRoot: t.TempDir(), GitStatusOK: true, WritePaths: []string{"src/a.go"}}
	if stopPolicyFingerprintCacheable(input) {
		t.Fatal("a repository without a readable policy lock must not reuse stop reports")
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

func TestCachedCleanStopWorkerCacheRetainsSmallExactFallback(t *testing.T) {
	counterPath := filepath.Join(t.TempDir(), "counter")
	repo := setupStopScriptPolicyRepo(t, counterPath, 0, "")
	state, err := InitializeSessionState(repo, "s-worker-exact-cache")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runStopPolicyCheck(repo, state); err != nil {
		t.Fatal(err)
	}
	state, err = LoadSessionState(repo, state.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	taskSnapshot, err := captureStopTaskSnapshot(repo)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := cachedCleanStopPolicyReportForEvidenceWithCache(
		repo, state, stopPolicyEvidenceHash(state), NewStopDecisionCache(), taskSnapshot,
	); !ok {
		t.Fatal("worker cache without a costly-state generation lost the exact clean fallback")
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
		{Path: "directory", WorktreeHash: "dir:" + strings.Repeat("d", 64)},
		{Path: "link", WorktreeHash: "symlink:" + strings.Repeat("b", 64)},
		{Path: "submodule", WorktreeHash: "submodule:" + strings.Repeat("c", 64), IndexEntry: "160000 abc 0"},
	} {
		if !completionDirtyFilesTrusted([]gitDirtyFile{file}) {
			t.Fatalf("safe dirty-file identity was rejected: %#v", file)
		}
	}
}

func TestStopPolicyFingerprintBindsUntrackedDirectoryContentInEveryMode(t *testing.T) {
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
	if normalA == normalB {
		t.Fatal("default normal mode must bind content below an untracked directory sentinel")
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

// TestStopPolicyFingerprintBindsScriptTargets closes the check-script hole: a
// require_script body is an input by definition, and Git binds it only while it
// is tracked. A gitignored check script could otherwise be rewritten while the
// stored report kept being served.
func TestStopPolicyFingerprintBindsScriptTargets(t *testing.T) {
	cases := []struct {
		name string
		lock string
	}{
		{
			name: "top-level require_script",
			lock: `{"rules":[{"id":"gate","kind":"require_script","script":"scripts/check.sh","when_paths":["**"]}]}`,
		},
		{
			name: "require_script nested in a composite rule",
			lock: `{"rules":[{"id":"gate","kind":"all_of","when_paths":["**"],"checks":[{"kind":"require_script","script":"scripts/check.sh"}]}]}`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			repo := t.TempDir()
			writePolicyLock(t, repo, tc.lock)
			script := filepath.Join(repo, "scripts", "check.sh")
			if err := os.MkdirAll(filepath.Dir(script), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(script, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
				t.Fatal(err)
			}
			before := hashStopPolicyFingerprintInput(stopPolicyFingerprintInputFor(repo, SessionState{SessionID: "s1", WritePaths: []string{"src/a.go"}}))
			if err := os.WriteFile(script, []byte("#!/bin/sh\nexit 2\n"), 0o755); err != nil {
				t.Fatal(err)
			}
			after := hashStopPolicyFingerprintInput(stopPolicyFingerprintInputFor(repo, SessionState{SessionID: "s1", WritePaths: []string{"src/a.go"}}))
			if before == "" || before == after {
				t.Fatal("rewriting the check script must move the fingerprint")
			}
		})
	}
}

// TestScriptCacheInputsDecideStopReuse is the contract TASK 147 introduces: a
// script body is opaque, so only the inputs its author declares make the plan a
// function of the fingerprint. An undeclared script plan is never reused.
func TestScriptCacheInputsDecideStopReuse(t *testing.T) {
	cases := []struct {
		name      string
		lock      string
		cacheable bool
	}{
		{
			name:      "undeclared script plan stays off the warm path",
			lock:      `{"rules":[{"id":"gate","kind":"require_script","script":"scripts/check.sh","when_paths":["**"]}]}`,
			cacheable: false,
		},
		{
			name:      "declared inputs make the plan cacheable",
			lock:      `{"rules":[{"id":"gate","kind":"require_script","script":"scripts/check.sh","cache_inputs":["build/report.json"],"when_paths":["**"]}]}`,
			cacheable: true,
		},
		{
			name:      "undeclared script check inside a composite rule",
			lock:      `{"rules":[{"id":"gate","kind":"all_of","checks":[{"kind":"require_script","script":"scripts/inner.sh"}]}]}`,
			cacheable: false,
		},
		{
			name:      "declared script check inside a composite rule",
			lock:      `{"rules":[{"id":"gate","kind":"all_of","checks":[{"kind":"require_script","script":"scripts/inner.sh","cache_inputs":["build/inner.json"]}]}]}`,
			cacheable: true,
		},
		{
			name:      "one undeclared script disqualifies the whole plan",
			lock:      `{"rules":[{"id":"a","kind":"require_script","script":"scripts/a.sh","cache_inputs":["build/a.json"],"when_paths":["**"]},{"id":"b","kind":"require_script","script":"scripts/b.sh","when_paths":["**"]}]}`,
			cacheable: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			repo := t.TempDir()
			writePolicyLock(t, repo, tc.lock)
			input := stopPolicyFingerprintInput{RepoRoot: repo, GitStatusOK: true, WritePaths: []string{"src/a.go"}}
			if got := stopPolicyFingerprintCacheable(input); got != tc.cacheable {
				t.Fatalf("cacheable = %v, want %v", got, tc.cacheable)
			}
		})
	}
}

// TestStopPolicyFingerprintBindsDeclaredScriptInputs proves the declaration is
// load-bearing: a declared input moves the fingerprint when it changes, so a
// stored report cannot survive the state its script inspects.
func TestStopPolicyFingerprintBindsDeclaredScriptInputs(t *testing.T) {
	repo := t.TempDir()
	writePolicyLock(t, repo, `{"rules":[{"id":"gate","kind":"require_script","script":"scripts/check.sh","cache_inputs":["build/report.json"],"when_paths":["**"]}]}`)
	declared := filepath.Join(repo, "build", "report.json")
	if err := os.MkdirAll(filepath.Dir(declared), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(declared, []byte(`{"status":"pass"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	before := hashStopPolicyFingerprintInput(stopPolicyFingerprintInputFor(repo, SessionState{SessionID: "s1", WritePaths: []string{"src/a.go"}}))
	if err := os.WriteFile(declared, []byte(`{"status":"fail"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	after := hashStopPolicyFingerprintInput(stopPolicyFingerprintInputFor(repo, SessionState{SessionID: "s1", WritePaths: []string{"src/a.go"}}))
	if before == "" || before == after {
		t.Fatal("rewriting a declared script input must move the fingerprint")
	}
	if err := os.Remove(declared); err != nil {
		t.Fatal(err)
	}
	if removed := hashStopPolicyFingerprintInput(stopPolicyFingerprintInputFor(repo, SessionState{SessionID: "s1", WritePaths: []string{"src/a.go"}})); removed == after {
		t.Fatal("deleting a declared script input must move the fingerprint")
	}
}

// TestDeclaredScriptInputMayBeADirectory keeps the declaration usable for the
// gates that inspect a surface rather than a single file. A declared directory
// contributes its bounded recursive content identity, so an added, removed, or
// rewritten file inside it invalidates the stored report.
func TestDeclaredScriptInputMayBeADirectory(t *testing.T) {
	repo := t.TempDir()
	writePolicyLock(t, repo, `{"rules":[{"id":"gate","kind":"require_script","script":"scripts/check.sh","cache_inputs":["docs/tasks"],"when_paths":["**"]}]}`)
	tasks := filepath.Join(repo, "docs", "tasks")
	if err := os.MkdirAll(tasks, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tasks, "001.md"), []byte("first\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	fingerprint := func() string {
		return hashStopPolicyFingerprintInput(stopPolicyFingerprintInputFor(repo, SessionState{SessionID: "s1", WritePaths: []string{"src/a.go"}}))
	}
	initial := fingerprint()
	if initial == "" {
		t.Fatal("fingerprint is empty")
	}
	if err := os.WriteFile(filepath.Join(tasks, "002.md"), []byte("second\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	added := fingerprint()
	if added == initial {
		t.Fatal("adding a file inside a declared directory must move the fingerprint")
	}
	if err := os.WriteFile(filepath.Join(tasks, "002.md"), []byte("rewritten\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	rewritten := fingerprint()
	if rewritten == added {
		t.Fatal("rewriting a file inside a declared directory must move the fingerprint")
	}
	if err := os.Remove(filepath.Join(tasks, "002.md")); err != nil {
		t.Fatal(err)
	}
	if removed := fingerprint(); removed != initial {
		t.Fatal("restoring a declared directory must restore its identity")
	}
}

// TestPolicyInputsSkipPathsTheDirtySetAlreadyBinds keeps the fingerprint from
// hashing the same path twice: a dirty file already contributes its exact
// content identity through the Git snapshot.
func TestPolicyInputsSkipPathsTheDirtySetAlreadyBinds(t *testing.T) {
	repo := t.TempDir()
	if err := os.WriteFile(filepath.Join(repo, "STATUS.md"), []byte("current\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	dirty := []gitDirtyFile{{Path: "STATUS.md", WorktreeHash: strings.Repeat("a", 64)}}
	if got := stopPolicyInputIdentities(repo, []string{"STATUS.md"}, dirty); got != nil {
		t.Fatalf("a dirty path must not be bound twice: %+v", got)
	}
	got := stopPolicyInputIdentities(repo, []string{"STATUS.md"}, nil)
	if len(got) != 1 || got[0].Path != "STATUS.md" || got[0].Identity == "" {
		t.Fatalf("a path Git does not report must be bound: %+v", got)
	}
	missing := stopPolicyInputIdentities(repo, []string{"build/absent.json"}, nil)
	if len(missing) != 1 || missing[0].Identity != "missing" {
		t.Fatalf("an absent declared input must bind as missing: %+v", missing)
	}
}

// TestExpiredReportLeavesThePersistentWorkerWarmPath proves the age boundary is
// enforced on the persistent-worker cache too, not only in session state.
func TestExpiredReportLeavesThePersistentWorkerWarmPath(t *testing.T) {
	cache := NewStopDecisionCache()
	root := t.TempDir()
	state := SessionState{
		SessionID:             "s-expired",
		StopPolicyFingerprint: "fingerprint",
		StopPolicyReportHash:  "report",
		StopPolicyExpiresAt:   time.Now().Add(-time.Minute).Unix(),
	}
	cache.store(root, state, "generation")
	if _, ok := cache.entry(root, state.SessionID); !ok {
		t.Fatal("cache entry was not stored")
	}
	if _, ok := cache.readStableReport(root, state, stopTaskSnapshot{}, stopPolicyGitSnapshot{}); ok {
		t.Fatal("an expired report must not be served from the warm path")
	}
	if _, ok := cache.entry(root, state.SessionID); ok {
		t.Fatal("an expired entry must be dropped, not left to be retried")
	}
}

// TestUnreachableScriptRulesDoNotDisqualifyReuse is what makes the declaration
// practical: a policy carries gates for many surfaces, and a Stop that touches
// none of a gate's when_paths never runs it. Only the gates this Stop can
// actually trigger decide whether its report may be reused.
func TestUnreachableScriptRulesDoNotDisqualifyReuse(t *testing.T) {
	const lock = `{"rules":[
		{"id":"docs-gate","kind":"require_script","script":"audits/docs.sh","when_paths":["docs/**"]},
		{"id":"code-gate","kind":"require_script","script":"audits/code.sh","cache_inputs":["build/report.json"],"when_paths":["src/**"]}
	]}`
	cases := []struct {
		name       string
		writePaths []string
		cacheable  bool
	}{
		{name: "only the declared gate is reachable", writePaths: []string{"src/a.go"}, cacheable: true},
		{name: "the undeclared gate is reachable", writePaths: []string{"docs/guide.md"}, cacheable: false},
		{name: "both are reachable", writePaths: []string{"src/a.go", "docs/guide.md"}, cacheable: false},
		{name: "no write reaches either gate", writePaths: []string{"README.md"}, cacheable: true},
		{name: "no writes at all", writePaths: nil, cacheable: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			repo := t.TempDir()
			writePolicyLock(t, repo, lock)
			input := stopPolicyFingerprintInput{RepoRoot: repo, GitStatusOK: true, WritePaths: tc.writePaths}
			if got := stopPolicyFingerprintCacheable(input); got != tc.cacheable {
				t.Fatalf("cacheable = %v, want %v", got, tc.cacheable)
			}
		})
	}
}

// TestScriptRuleReachabilityFailsTowardTriggerable keeps the scoping
// conservative: anything this pass cannot decide statically counts as
// reachable, so uncertainty never admits a report a gate might have changed.
func TestScriptRuleReachabilityFailsTowardTriggerable(t *testing.T) {
	cases := []struct {
		name       string
		whenPaths  []string
		writePaths []string
		want       bool
	}{
		{name: "no when_paths applies everywhere", whenPaths: nil, writePaths: []string{"src/a.go"}, want: true},
		{name: "templated pattern cannot be decided", whenPaths: []string{"docs/tasks/{task_id}.md"}, writePaths: []string{"src/a.go"}, want: true},
		{name: "malformed pattern cannot be decided", whenPaths: []string{"["}, writePaths: []string{"src/a.go"}, want: true},
		{name: "plain miss", whenPaths: []string{"docs/**"}, writePaths: []string{"src/a.go"}, want: false},
		{name: "plain hit", whenPaths: []string{"src/**"}, writePaths: []string{"src/a.go"}, want: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := stopPolicyRuleReachable(tc.whenPaths, tc.writePaths); got != tc.want {
				t.Fatalf("stopPolicyRuleReachable(%v, %v) = %v, want %v", tc.whenPaths, tc.writePaths, got, tc.want)
			}
		})
	}
}

// TestCompletionFingerprintBindsPolicyInputsEvenWhenNotCacheable guards a trap
// this pass walked into once: the same fingerprint identifies the completion
// candidate, so policy-named inputs must stay bound even for a policy whose
// Stop reports are never reused. Skipping that work as an optimisation would
// let a candidate survive a change to evidence the policy names.
func TestCompletionFingerprintBindsPolicyInputsEvenWhenNotCacheable(t *testing.T) {
	repo := t.TempDir()
	// One declared gate plus one undeclared gate: the plan is not cacheable,
	// and the declared input must still be bound.
	writePolicyLock(t, repo, `{"rules":[
		{"id":"declared","kind":"require_script","script":"audits/a.sh","cache_inputs":["build/report.json"],"when_paths":["src/**"]},
		{"id":"undeclared","kind":"require_script","script":"audits/b.sh","when_paths":["src/**"]}
	]}`)
	declared := filepath.Join(repo, "build", "report.json")
	if err := os.MkdirAll(filepath.Dir(declared), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(declared, []byte(`{"status":"pass"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	state := SessionState{SessionID: "s-completion", WritePaths: []string{"src/a.go"}}
	if stopPolicyFingerprintCacheable(stopPolicyFingerprintInputFor(repo, state)) {
		t.Fatal("fixture must describe a non-cacheable plan")
	}

	before := hashStopPolicyFingerprintInput(stopPolicyFingerprintInputFor(repo, state))
	if err := os.WriteFile(declared, []byte(`{"status":"fail"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if after := hashStopPolicyFingerprintInput(stopPolicyFingerprintInputFor(repo, state)); after == before {
		t.Fatal("a policy-named input must stay bound when the plan is not cacheable")
	}
}

// TestGitRefResolutionCoversLooseAndPackedRefs pins HEAD resolution, which the
// Stop fingerprint binds directly. A repository that has been packed keeps no
// loose ref file, so a resolver that only reads loose refs would silently bind
// an empty HEAD and make every Stop look identical.
func TestGitRefResolutionCoversLooseAndPackedRefs(t *testing.T) {
	const ref = "refs/heads/main"
	const objectID = "9f1c0d5cba1c1f9b6f2f5cfd8a2a4a2f1b0d3c4e"
	cleanRef, err := cleanGitRefPath(ref)
	if err != nil {
		t.Fatalf("clean ref: %v", err)
	}

	t.Run("loose ref wins", func(t *testing.T) {
		root := t.TempDir()
		if err := os.MkdirAll(filepath.Join(root, "refs", "heads"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, cleanRef), []byte(objectID+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, "packed-refs"), []byte("0000000000000000000000000000000000000000 "+ref+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		got, found, err := readLooseGitRef([]string{root}, cleanRef)
		if err != nil || !found || got != objectID {
			t.Fatalf("loose ref = %q found=%v err=%v", got, found, err)
		}
	})

	t.Run("packed ref is used when no loose ref exists", func(t *testing.T) {
		root := t.TempDir()
		body := "# pack-refs with: peeled fully-peeled sorted\n" +
			objectID + " " + ref + "\n" +
			"1111111111111111111111111111111111111111 refs/heads/other\n"
		if err := os.WriteFile(filepath.Join(root, "packed-refs"), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, found, err := readLooseGitRef([]string{root}, cleanRef); err != nil || found {
			t.Fatalf("loose lookup found=%v err=%v, want a miss", found, err)
		}
		got, found, err := readPackedGitRef([]string{root}, ref)
		if err != nil || !found || got != objectID {
			t.Fatalf("packed ref = %q found=%v err=%v", got, found, err)
		}
	})

	t.Run("peeled tag lines never match", func(t *testing.T) {
		root := t.TempDir()
		body := "2222222222222222222222222222222222222222 refs/tags/v1\n^" + objectID + "\n"
		if err := os.WriteFile(filepath.Join(root, "packed-refs"), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, found, _ := readPackedGitRef([]string{root}, "refs/heads/main"); found {
			t.Fatal("an unrelated packed entry must not resolve the requested ref")
		}
	})

	t.Run("absent packed-refs is a clean miss", func(t *testing.T) {
		got, found, err := readPackedGitRef([]string{t.TempDir()}, ref)
		if err != nil || found || got != "" {
			t.Fatalf("absent packed-refs = %q found=%v err=%v", got, found, err)
		}
	})

	t.Run("escaping refs are refused", func(t *testing.T) {
		// Every rooting and escape convention, on every platform: the decision
		// must not depend on which operating system evaluates the ref.
		for _, unsafe := range []string{
			"../outside", "/etc/passwd", "..",
			`..\outside`, `\etc\passwd`, `C:\refs\heads\main`, `\\server\share\ref`,
			"refs/../../../etc/passwd",
		} {
			if _, err := cleanGitRefPath(unsafe); err == nil {
				t.Fatalf("unsafe ref %q was accepted", unsafe)
			}
		}
	})
}

// TestWorktreePathHashDistinguishesEveryPathShape covers the primitive that
// binds both dirty files and policy-named inputs. Each shape must produce a
// distinct, stable identity so a swap between them invalidates a report.
func TestWorktreePathHashDistinguishesEveryPathShape(t *testing.T) {
	repo := t.TempDir()
	if err := os.WriteFile(filepath.Join(repo, "file.txt"), []byte("body\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(repo, "dir"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "dir", "inner.txt"), []byte("inner\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	regular := worktreePathHash(repo, "file.txt", "")
	directory := worktreePathHash(repo, "dir", "")
	missing := worktreePathHash(repo, "absent.txt", "")
	if missing != "missing" {
		t.Fatalf("absent path identity = %q, want missing", missing)
	}
	if regular == directory || regular == missing || directory == missing {
		t.Fatalf("path shapes share an identity: file=%q dir=%q missing=%q", regular, directory, missing)
	}
	if again := worktreePathHash(repo, "file.txt", ""); again != regular {
		t.Fatalf("identity is not stable: %q vs %q", regular, again)
	}
	if err := os.WriteFile(filepath.Join(repo, "file.txt"), []byte("changed\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if changed := worktreePathHash(repo, "file.txt", ""); changed == regular {
		t.Fatal("rewriting a file must change its identity")
	}
}
