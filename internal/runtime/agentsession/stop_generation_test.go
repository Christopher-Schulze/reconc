package agentsession

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"reconc.dev/reconc/internal/runtime"
)

func TestStopGenerationCacheReusesStableDirtyContent(t *testing.T) {
	counterPath := filepath.Join(t.TempDir(), "counter")
	repo := setupStopScriptPolicyRepo(t, counterPath, 0, "")
	trackStopGenerationBytes(t, repo, "src/a.go", bytes.Repeat([]byte{'a'}, stopGenerationMinBytes))
	if err := os.WriteFile(filepath.Join(repo, "src", "a.go"), bytes.Repeat([]byte{'b'}, stopGenerationMinBytes), 0o644); err != nil {
		t.Fatal(err)
	}
	state, err := InitializeSessionState(repo, "generation-hit")
	if err != nil {
		t.Fatal(err)
	}
	state, err = MutateSessionState(repo, state.SessionID, func(current SessionState) SessionState {
		return AppendWritePath(current, "src/a.go")
	})
	if err != nil {
		t.Fatal(err)
	}
	cache := NewStopDecisionCache()
	evaluator := runtime.NewEvaluator()
	first, err := runStopPolicyCheckWithSnapshotWithEvaluatorAndCache(repo, state, evaluator, cache, nil)
	if err != nil {
		t.Fatal(err)
	}
	if first.GenerationHit {
		t.Fatal("cold Stop unexpectedly used a generation entry")
	}
	state, err = LoadSessionState(repo, state.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	second, err := runStopPolicyCheckWithSnapshotWithEvaluatorAndCache(repo, state, evaluator, cache, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !second.GenerationHit {
		t.Fatal("stable dirty Stop did not use the generation fast path")
	}
	if got := readCounter(t, counterPath); got != 1 {
		t.Fatalf("stable generation reran policy script %d times, want 1", got)
	}
}

func TestStopGenerationCacheDetectsSameSizeSameMtimeRewrite(t *testing.T) {
	counterPath := filepath.Join(t.TempDir(), "counter")
	repo := setupStopScriptPolicyRepo(t, counterPath, 0, "")
	trackStopGenerationBytes(t, repo, "src/a.go", bytes.Repeat([]byte{'a'}, stopGenerationMinBytes))
	target := filepath.Join(repo, "src", "a.go")
	if err := os.WriteFile(target, bytes.Repeat([]byte{'b'}, stopGenerationMinBytes), 0o644); err != nil {
		t.Fatal(err)
	}
	fixed := time.Date(2026, 8, 5, 12, 0, 0, 0, time.UTC)
	if err := os.Chtimes(target, fixed, fixed); err != nil {
		t.Fatal(err)
	}
	state, err := InitializeSessionState(repo, "generation-rewrite")
	if err != nil {
		t.Fatal(err)
	}
	state, err = MutateSessionState(repo, state.SessionID, func(current SessionState) SessionState {
		return AppendWritePath(current, "src/a.go")
	})
	if err != nil {
		t.Fatal(err)
	}
	cache := NewStopDecisionCache()
	evaluator := runtime.NewEvaluator()
	if _, err := runStopPolicyCheckWithSnapshotWithEvaluatorAndCache(repo, state, evaluator, cache, nil); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, bytes.Repeat([]byte{'c'}, stopGenerationMinBytes), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(target, fixed, fixed); err != nil {
		t.Fatal(err)
	}
	state, err = LoadSessionState(repo, state.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	result, err := runStopPolicyCheckWithSnapshotWithEvaluatorAndCache(repo, state, evaluator, cache, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.GenerationHit {
		t.Fatal("same-size, same-mtime rewrite reused stale generation")
	}
	if got := readCounter(t, counterPath); got != 2 {
		t.Fatalf("rewritten dirty content ran policy script %d times, want 2", got)
	}
}

func TestStopGenerationCacheDetectsDeclaredInputRewrite(t *testing.T) {
	counterPath := filepath.Join(t.TempDir(), "counter")
	repo := setupStopScriptPolicyRepo(t, counterPath, 0, "")
	trackStopGenerationBytes(t, repo, "src/a.go", bytes.Repeat([]byte{'a'}, stopGenerationMinBytes))
	if err := os.WriteFile(filepath.Join(repo, "src", "a.go"), bytes.Repeat([]byte{'b'}, stopGenerationMinBytes), 0o644); err != nil {
		t.Fatal(err)
	}
	declaredInput := filepath.Join(repo, "build", "report.json")
	fixed := time.Date(2026, 8, 5, 12, 0, 0, 0, time.UTC)
	if err := os.Chtimes(declaredInput, fixed, fixed); err != nil {
		t.Fatal(err)
	}
	state, err := InitializeSessionState(repo, "generation-declared-input")
	if err != nil {
		t.Fatal(err)
	}
	state, err = MutateSessionState(repo, state.SessionID, func(current SessionState) SessionState {
		return AppendWritePath(current, "src/a.go")
	})
	if err != nil {
		t.Fatal(err)
	}
	cache := NewStopDecisionCache()
	evaluator := runtime.NewEvaluator()
	if _, err := runStopPolicyCheckWithSnapshotWithEvaluatorAndCache(repo, state, evaluator, cache, nil); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(declaredInput, []byte(`{"status":"fail"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(declaredInput, fixed, fixed); err != nil {
		t.Fatal(err)
	}
	state, err = LoadSessionState(repo, state.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	result, err := runStopPolicyCheckWithSnapshotWithEvaluatorAndCache(repo, state, evaluator, cache, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.GenerationHit {
		t.Fatal("same-size, same-mtime declared-input rewrite reused a stale generation")
	}
	if got := readCounter(t, counterPath); got != 2 {
		t.Fatalf("rewritten declared input ran policy script %d times, want 2", got)
	}
}

func TestStopGenerationReevaluatesConcurrentEvidenceBeforeWarming(t *testing.T) {
	counterPath := filepath.Join(t.TempDir(), "counter")
	repo := setupStopScriptPolicyRepo(t, counterPath, 0, "")
	trackStopGenerationBytes(t, repo, "src/a.go", bytes.Repeat([]byte{'a'}, stopGenerationMinBytes))
	if err := os.WriteFile(filepath.Join(repo, "src", "a.go"), bytes.Repeat([]byte{'b'}, stopGenerationMinBytes), 0o644); err != nil {
		t.Fatal(err)
	}
	state, err := InitializeSessionState(repo, "generation-concurrent-evidence")
	if err != nil {
		t.Fatal(err)
	}
	state, err = MutateSessionState(repo, state.SessionID, func(current SessionState) SessionState {
		return AppendWritePath(current, "src/a.go")
	})
	if err != nil {
		t.Fatal(err)
	}
	cache := NewStopDecisionCache()
	evaluator := runtime.NewEvaluator()
	if _, err := runStopPolicyCheckWithSnapshotWithEvaluatorAndCache(repo, state, evaluator, cache, nil); err != nil {
		t.Fatal(err)
	}
	state, err = LoadSessionState(repo, state.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	started := filepath.Join(t.TempDir(), "started")
	release := filepath.Join(t.TempDir(), "release")
	script := fmt.Sprintf(
		"#!/bin/sh\n: > %q\nwhile [ ! -f %q ]; do sleep 0.05; done\nprintf x >> %q\nexit 0\n",
		started, release, counterPath,
	)
	if err := os.WriteFile(filepath.Join(repo, ".reconc", "scripts", "stop-gate.sh"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	type stopResult struct {
		result stopPolicyCheckResult
		err    error
	}
	resultCh := make(chan stopResult, 1)
	go func() {
		result, runErr := runStopPolicyCheckWithSnapshotWithEvaluatorAndCache(repo, state, evaluator, cache, nil)
		resultCh <- stopResult{result: result, err: runErr}
	}()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, statErr := os.Stat(started); statErr == nil {
			break
		} else if !os.IsNotExist(statErr) {
			t.Fatal(statErr)
		}
		if time.Now().After(deadline) {
			t.Fatal("Stop policy script did not reach the concurrency barrier")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if _, err := MutateSessionState(repo, state.SessionID, func(current SessionState) SessionState {
		current.Claims = append(current.Claims, "concurrent-evidence")
		current.EvidenceEpoch++
		return current
	}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(release, []byte("release\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	concurrent := <-resultCh
	if concurrent.err != nil {
		t.Fatal(concurrent.err)
	}
	if concurrent.result.GenerationHit {
		t.Fatal("changed policy script unexpectedly reused the generation cache")
	}
	state, err = LoadSessionState(repo, state.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	if state.StopPolicyFingerprint == "" || state.StopPolicyEvidenceHash == "" || state.StopPolicyReportHash == "" {
		t.Fatalf("reevaluated concurrent evidence was not cached: %+v", state)
	}
	if got := stopPolicyEvidenceHash(state); state.StopPolicyEvidenceHash != got {
		t.Fatalf("cached evidence hash = %q, want current %q", state.StopPolicyEvidenceHash, got)
	}
	result, err := runStopPolicyCheckWithSnapshotWithEvaluatorAndCache(repo, state, evaluator, cache, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !result.GenerationHit {
		t.Fatal("report reevaluated with concurrent evidence did not become the warm generation")
	}
	if got := readCounter(t, counterPath); got != 3 {
		t.Fatalf("concurrent-evidence sequence ran policy script %d times, want 3", got)
	}
}

func TestStopGenerationCacheSupportsSealedEvidenceSegments(t *testing.T) {
	counterPath := filepath.Join(t.TempDir(), "counter")
	repo := setupStopScriptPolicyRepo(t, counterPath, 0, "")
	trackStopGenerationBytes(t, repo, "src/a.go", bytes.Repeat([]byte{'a'}, stopGenerationMinBytes))
	if err := os.WriteFile(filepath.Join(repo, "src", "a.go"), bytes.Repeat([]byte{'b'}, stopGenerationMinBytes), 0o644); err != nil {
		t.Fatal(err)
	}
	const sessionID = "generation-sealed-evidence"
	if _, err := InitializeSessionState(repo, sessionID); err != nil {
		t.Fatal(err)
	}
	if _, err := MutateSessionState(repo, sessionID, func(current SessionState) SessionState {
		return AppendWritePath(current, "src/a.go")
	}); err != nil {
		t.Fatal(err)
	}
	seed, err := LoadSessionState(repo, sessionID)
	if err != nil {
		t.Fatal(err)
	}
	root := seed.RepoRoot
	if err := withSessionLock(root, sessionID, func() error {
		current, loadErr := loadSessionStateResolved(root, sessionID)
		if loadErr != nil {
			return loadErr
		}
		rotated, rotateErr := rotateSessionEvidenceLocked(root, current)
		if rotateErr != nil {
			return rotateErr
		}
		return saveSessionStateLocked(rotated)
	}); err != nil {
		t.Fatal(err)
	}
	state, err := LoadSessionState(repo, sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if state.EvidenceSegmentCount != 1 || len(state.WritePaths) != 0 {
		t.Fatalf("fixture did not seal its write evidence: %+v", state)
	}
	cache := NewStopDecisionCache()
	evaluator := runtime.NewEvaluator()
	if _, err := runStopPolicyCheckWithSnapshotWithEvaluatorAndCache(repo, state, evaluator, cache, nil); err != nil {
		t.Fatal(err)
	}
	state, err = LoadSessionState(repo, sessionID)
	if err != nil {
		t.Fatal(err)
	}
	result, err := runStopPolicyCheckWithSnapshotWithEvaluatorAndCache(repo, state, evaluator, cache, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !result.GenerationHit {
		t.Fatal("unchanged sealed evidence did not use the generation cache")
	}
	if got := readCounter(t, counterPath); got != 1 {
		t.Fatalf("sealed evidence ran policy script %d times, want 1", got)
	}
}

func TestStopGenerationCacheCoalescesConcurrentEquivalentStops(t *testing.T) {
	counterPath := filepath.Join(t.TempDir(), "counter")
	repo := setupStopScriptPolicyRepo(t, counterPath, 0, "")
	trackStopGenerationBytes(t, repo, "src/a.go", bytes.Repeat([]byte{'a'}, stopGenerationMinBytes))
	if err := os.WriteFile(filepath.Join(repo, "src", "a.go"), bytes.Repeat([]byte{'b'}, stopGenerationMinBytes), 0o644); err != nil {
		t.Fatal(err)
	}
	state, err := InitializeSessionState(repo, "generation-concurrent")
	if err != nil {
		t.Fatal(err)
	}
	state, err = MutateSessionState(repo, state.SessionID, func(current SessionState) SessionState {
		return AppendWritePath(current, "src/a.go")
	})
	if err != nil {
		t.Fatal(err)
	}
	cache := NewStopDecisionCache()
	evaluator := runtime.NewEvaluator()
	const workers = 8
	var wait sync.WaitGroup
	errorsOut := make(chan error, workers)
	for range workers {
		wait.Add(1)
		go func() {
			defer wait.Done()
			_, runErr := runStopPolicyCheckWithSnapshotWithEvaluatorAndCache(repo, state, evaluator, cache, nil)
			errorsOut <- runErr
		}()
	}
	wait.Wait()
	close(errorsOut)
	for runErr := range errorsOut {
		if runErr != nil {
			t.Error(runErr)
		}
	}
	if got := readCounter(t, counterPath); got != 1 {
		t.Fatalf("equivalent concurrent Stops ran policy script %d times, want 1", got)
	}
}

func TestStopGenerationCacheRejectsInterruptedReportPublication(t *testing.T) {
	counterPath := filepath.Join(t.TempDir(), "counter")
	repo := setupStopScriptPolicyRepo(t, counterPath, 0, "")
	trackStopGenerationBytes(t, repo, "src/a.go", bytes.Repeat([]byte{'a'}, stopGenerationMinBytes))
	if err := os.WriteFile(filepath.Join(repo, "src", "a.go"), bytes.Repeat([]byte{'b'}, stopGenerationMinBytes), 0o644); err != nil {
		t.Fatal(err)
	}
	state, err := InitializeSessionState(repo, "generation-report")
	if err != nil {
		t.Fatal(err)
	}
	state, err = MutateSessionState(repo, state.SessionID, func(current SessionState) SessionState {
		return AppendWritePath(current, "src/a.go")
	})
	if err != nil {
		t.Fatal(err)
	}
	cache := NewStopDecisionCache()
	evaluator := runtime.NewEvaluator()
	if _, err := runStopPolicyCheckWithSnapshotWithEvaluatorAndCache(repo, state, evaluator, cache, nil); err != nil {
		t.Fatal(err)
	}
	state, err = LoadSessionState(repo, state.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(state.ReportPath, []byte("{truncated"), 0o600); err != nil {
		t.Fatal(err)
	}
	result, err := runStopPolicyCheckWithSnapshotWithEvaluatorAndCache(repo, state, evaluator, cache, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.GenerationHit {
		t.Fatal("truncated report was reused by generation cache")
	}
	if got := readCounter(t, counterPath); got != 2 {
		t.Fatalf("truncated report ran policy script %d times, want 2", got)
	}
}

func TestStopGenerationCacheIsBounded(t *testing.T) {
	cache := NewStopDecisionCache()
	for index := 0; index < maxStopDecisionCacheEntries+5; index++ {
		state := emptyState("/repo", fmt.Sprintf("session-%d", index))
		state.StopPolicyFingerprint = "fingerprint"
		state.StopPolicyReportHash = "report"
		cache.store("/repo", state, "generation")
	}
	if len(cache.entries) != maxStopDecisionCacheEntries || len(cache.order) != maxStopDecisionCacheEntries {
		t.Fatalf("cache bounds: entries=%d order=%d", len(cache.entries), len(cache.order))
	}
}

func TestStopGenerationWorthwhileRejectsMixedDirtySubmodule(t *testing.T) {
	repo := t.TempDir()
	largePath := filepath.Join(repo, "large.bin")
	if err := os.WriteFile(largePath, bytes.Repeat([]byte{'x'}, stopGenerationMinBytes), 0o644); err != nil {
		t.Fatal(err)
	}
	files := []gitDirtyFile{
		{Path: "large.bin", WorktreeHash: strings.Repeat("a", 64)},
		{Path: "modules/child", WorktreeHash: "submodule:" + strings.Repeat("b", 64), IndexEntry: "160000 abc 0"},
	}
	if stopGenerationWorthwhile(repo, files) {
		t.Fatal("a dirty submodule must keep the entire Stop on the exact path")
	}
}

func TestStopRepositoryGenerationBindsPolicyTaskIndexHeadAndSymlink(t *testing.T) {
	repo := setupStopBenchmarkRepo(t)
	taskSnapshot, err := captureStopTaskSnapshot(repo)
	if err != nil {
		t.Fatal(err)
	}
	gitSnapshot := stopPolicyGitSnapshotFor(repo)
	initial := requireStopGeneration(t, repo, gitSnapshot, taskSnapshot)

	policyPath := filepath.Join(repo, "policies", "rules.yml")
	if err := os.WriteFile(policyPath, []byte("rules: []\n# changed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	policyChanged := requireStopGeneration(t, repo, gitSnapshot, taskSnapshot)
	if policyChanged.Fingerprint == initial.Fingerprint {
		t.Fatal("policy source content did not invalidate the generation")
	}
	if err := os.WriteFile(policyPath, []byte("rules: []\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	changedTask := taskSnapshot
	changedTask.State.Blocker = "changed TASK snapshot"
	taskChanged := requireStopGeneration(t, repo, gitSnapshot, changedTask)
	if taskChanged.Fingerprint == initial.Fingerprint {
		t.Fatal("TASK state did not invalidate the generation")
	}

	if err := os.WriteFile(filepath.Join(repo, "src", "a.go"), []byte("package src // staged\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	stopGenerationGit(t, repo, "add", "src/a.go")
	indexSnapshot := stopPolicyGitSnapshotFor(repo)
	indexChanged := requireStopGeneration(t, repo, indexSnapshot, taskSnapshot)
	if indexChanged.Fingerprint == initial.Fingerprint {
		t.Fatal("Git index transition did not invalidate the generation")
	}
	stopGenerationGit(t, repo, "commit", "-m", "advance head", "--quiet")
	headSnapshot := stopPolicyGitSnapshotFor(repo)
	headChanged := requireStopGeneration(t, repo, headSnapshot, taskSnapshot)
	if headChanged.Fingerprint == indexChanged.Fingerprint {
		t.Fatal("Git HEAD transition did not invalidate the generation")
	}

	link := filepath.Join(repo, "untracked-link")
	if err := os.Symlink("target-one", link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	linkSnapshot := stopPolicyGitSnapshotFor(repo)
	linkOne := requireStopGeneration(t, repo, linkSnapshot, taskSnapshot)
	if err := os.Remove(link); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("target-two", link); err != nil {
		t.Fatal(err)
	}
	linkTwo := requireStopGeneration(t, repo, stopPolicyGitSnapshotFor(repo), taskSnapshot)
	if linkTwo.Fingerprint == linkOne.Fingerprint {
		t.Fatal("same-path symlink target change did not invalidate the generation")
	}
}

func requireStopGeneration(
	t *testing.T,
	repo string,
	gitSnapshot stopPolicyGitSnapshot,
	taskSnapshot stopTaskSnapshot,
) stopGenerationCapture {
	t.Helper()
	generation, ok := captureStopRepositoryGeneration(repo, gitSnapshot, taskSnapshot, nil)
	if !ok || generation.Fingerprint == "" {
		t.Fatal("repository generation capture failed")
	}
	return generation
}

func stopGenerationGit(t *testing.T, repo string, args ...string) {
	t.Helper()
	if output, err := exec.Command("git", append([]string{"-C", repo}, args...)...).CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, output)
	}
}

func trackStopGenerationBytes(t *testing.T, repo, rel string, body []byte) {
	t.Helper()
	path := filepath.Join(repo, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"add", rel}, {"commit", "-m", "track generation fixture", "--quiet"}} {
		if output, err := exec.Command("git", append([]string{"-C", repo}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, output)
		}
	}
}
