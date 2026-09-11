package agentsession

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"reconc.dev/reconc/internal/compiler"
	"reconc.dev/reconc/internal/runtime"
)

func TestPreDecisionCacheRejectsRetargetedWriteAncestor(t *testing.T) {
	for _, handler := range []HookHandler{HookHandlerPreToolUse, HookHandlerPermissionRequest} {
		t.Run(string(handler), func(t *testing.T) {
			repo := setupPolicyRepo(t)
			root, err := ResolveRepoRootRef(repo)
			if err != nil {
				t.Fatal(err)
			}
			for _, directory := range []string{"src", "generated"} {
				if err := os.Mkdir(filepath.Join(root.Path(), directory), 0o755); err != nil {
					t.Fatal(err)
				}
			}
			link := filepath.Join(root.Path(), "target")
			if err := os.Symlink(filepath.Join(root.Path(), "src"), link); err != nil {
				t.Fatal(err)
			}
			if start := RunHookRequest(root, HookHandlerSessionStart, "claude-session-start", []byte(`{"session_id":"retarget"}`)); start.ExitCode != 0 {
				t.Fatalf("session start failed: %+v", start)
			}
			payload := []byte(`{"session_id":"retarget","tool_use_id":"same-call","tool_name":"Write","tool_input":{"file_path":"target/main.go"}}`)
			warm := RunHookRequest(root, HookHandlerPreToolUse, "claude-pre-tool-use", payload)
			if warm.ExitCode != 0 {
				t.Fatalf("initial safe write denied: %+v", warm)
			}
			if _, err := os.Stat(preDecisionCachePath(root.Path(), payload)); err != nil {
				t.Fatalf("initial allow was not cached: %v", err)
			}
			if err := os.Rename(link, link+"-previous"); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(filepath.Join(root.Path(), "generated"), link); err != nil {
				t.Fatal(err)
			}
			cached := RunHookRequest(root, handler, "claude-pre-tool-use", payload)
			fresh := runPreToolUseResolved(root.Path(), payload)
			if fresh.ExitCode != 2 {
				t.Fatalf("uncached write to protected target was not denied: %+v", fresh)
			}
			fresh = adaptPreDecision(fresh, handler == HookHandlerPermissionRequest)
			if cached.ExitCode != fresh.ExitCode || cached.Stdout != fresh.Stdout || cached.Stderr != fresh.Stderr {
				t.Fatalf("retargeted ancestor reused stale decision: cached=%+v fresh=%+v", cached, fresh)
			}
		})
	}
}

func TestPreDecisionCacheRejectsChangedEvidenceSegment(t *testing.T) {
	for _, mutation := range []string{"missing", "truncated", "corrupt"} {
		t.Run(mutation, func(t *testing.T) {
			repo := setupPolicyRepo(t)
			root, err := ResolveRepoRootRef(repo)
			if err != nil {
				t.Fatal(err)
			}
			if start := RunHookRequest(root, HookHandlerSessionStart, "claude-session-start", []byte(`{"session_id":"segments"}`)); start.ExitCode != 0 {
				t.Fatalf("session start failed: %+v", start)
			}
			appendCommandRange(t, root.Path(), "segments", 0, maxCommandEvidenceItems)
			appendCommandRange(t, root.Path(), "segments", maxCommandEvidenceItems, 1)
			state, err := LoadSessionState(root.Path(), "segments")
			if err != nil || state.EvidenceSegmentCount == 0 {
				t.Fatalf("real evidence rotation failed: segments=%d error=%v", state.EvidenceSegmentCount, err)
			}
			payload := []byte(`{"session_id":"segments","tool_use_id":"same-call","tool_name":"Write","tool_input":{"file_path":"src/main.go"}}`)
			if warm := RunHookRequest(root, HookHandlerPreToolUse, "claude-pre-tool-use", payload); warm.ExitCode != 0 {
				t.Fatalf("initial write denied: %+v", warm)
			}
			if _, err := os.Stat(preDecisionCachePath(root.Path(), payload)); err != nil {
				t.Fatalf("initial allow was not cached: %v", err)
			}
			path := evidenceSegmentPath(root.Path(), state.SessionID, 1)
			if mutation == "missing" {
				if err := os.Rename(path, path+"-previous"); err != nil {
					t.Fatal(err)
				}
			} else {
				body, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				if mutation == "truncated" {
					body = body[:len(body)/2]
				} else {
					body[len(body)/2] ^= 1
				}
				if err := os.WriteFile(path, body, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			cached := RunHookRequest(root, HookHandlerPreToolUse, "claude-pre-tool-use", payload)
			fresh := runPreToolUseResolved(root.Path(), payload)
			if fresh.ExitCode != 2 {
				t.Fatalf("uncached evaluation accepted changed evidence: %+v", fresh)
			}
			if cached.ExitCode != fresh.ExitCode || cached.Stdout != fresh.Stdout || cached.Stderr != fresh.Stderr {
				t.Fatalf("changed evidence reused stale allow: cached=%+v fresh=%+v", cached, fresh)
			}
		})
	}
}

func TestPreDecisionCacheBindsExistingDependencyContent(t *testing.T) {
	repo := setupPolicyRepo(t)
	root, err := ResolveRepoRootRef(repo)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root.Path(), "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root.Path(), "src", "main.go")
	if err := os.WriteFile(path, []byte("package a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if start := RunHookRequest(root, HookHandlerSessionStart, "claude-session-start", []byte(`{"session_id":"content"}`)); start.ExitCode != 0 {
		t.Fatalf("session start failed: %+v", start)
	}
	payload := []byte(`{"session_id":"content","tool_use_id":"same-call","tool_name":"Write","tool_input":{"file_path":"src/main.go"}}`)
	before, ok := preDecisionKey(root.Path(), payload)
	if !ok {
		t.Fatal("initial dependency snapshot was not cacheable")
	}
	if warm := RunHookRequest(root, HookHandlerPreToolUse, "claude-pre-tool-use", payload); warm.ExitCode != 0 {
		t.Fatalf("initial write denied: %+v", warm)
	}
	if err := os.WriteFile(path, []byte("package b\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	after, ok := preDecisionKey(root.Path(), payload)
	if !ok || before == after {
		t.Fatalf("dependency content mutation did not change key: before=%q after=%q cacheable=%v", before, after, ok)
	}
	if cached := RunHookRequest(root, HookHandlerPreToolUse, "claude-pre-tool-use", payload); cached.ExitCode != 0 {
		t.Fatalf("freshly resampled dependency unexpectedly denied: %+v", cached)
	}
}

func TestPreDecisionCacheBindsAbsentParentCreation(t *testing.T) {
	repo := setupPolicyRepo(t)
	root, err := ResolveRepoRootRef(repo)
	if err != nil {
		t.Fatal(err)
	}
	if start := RunHookRequest(root, HookHandlerSessionStart, "claude-session-start", []byte(`{"session_id":"parent"}`)); start.ExitCode != 0 {
		t.Fatalf("session start failed: %+v", start)
	}
	payload := []byte(`{"session_id":"parent","tool_use_id":"same-call","tool_name":"Write","tool_input":{"file_path":"future/main.go"}}`)
	before, ok := preDecisionKey(root.Path(), payload)
	if !ok {
		t.Fatal("initial absent-parent snapshot was not cacheable")
	}
	if err := os.Mkdir(filepath.Join(root.Path(), "future"), 0o755); err != nil {
		t.Fatal(err)
	}
	after, ok := preDecisionKey(root.Path(), payload)
	if !ok || before == after {
		t.Fatalf("absent-parent creation did not change key: before=%q after=%q cacheable=%v", before, after, ok)
	}
}

func TestPreDecisionIdentityStableForExistingPath(t *testing.T) {
	repo := setupStopBenchmarkRepo(t)
	payload := &HookPayload{
		SessionID: "stable", ToolUseID: "call", ToolName: "Write",
		ToolInput: map[string]interface{}{"file_path": "src/a.go"},
	}
	initial, ok := preDecisionInputsForPayload(repo, payload)
	if !ok {
		t.Fatal("initial identity was not cacheable")
	}
	resampled, ok := resamplePreDecisionInputs(repo, payload, initial)
	if !ok || !initial.identity.equal(resampled.identity) {
		t.Fatalf("stable existing path identity changed: initial=%+v resampled=%+v cacheable=%v", initial.identity, resampled.identity, ok)
	}
}

func TestWorkerPreDecisionHooksReuseVerifiedEvidencePrefix(t *testing.T) {
	repo := setupStopBenchmarkRepo(t)
	root, err := ResolveRepoRootRef(repo)
	if err != nil {
		t.Fatal(err)
	}
	const sessionID = "worker-prefix"
	state := writeEvidenceChainFixture(t, repo, sessionID, 2, 128)
	if err := SaveSessionState(state); err != nil {
		t.Fatal(err)
	}
	payload := []byte(`{"session_id":"worker-prefix","tool_use_id":"worker-call","tool_name":"Write","tool_input":{"file_path":"src/a.go"}}`)
	evaluator := runtime.NewEvaluator()
	cache := NewStopDecisionCache()
	if result := RunHookRequestWithEvaluatorAndStopCache(root, HookHandlerPreToolUse, "claude-pre-tool-use", payload, evaluator, cache); result.ExitCode != 0 {
		t.Fatalf("cold worker pre-hook failed: %+v", result)
	}
	prefix, ok := cache.verifiedEvidencePrefix(root.Path(), sessionID)
	if !ok || prefix.count != 2 || len(prefix.segments) != 2 {
		t.Fatalf("cold worker pre-hook did not cache the complete prefix: ok=%t prefix=%+v", ok, prefix)
	}
	if result := RunHookRequestWithEvaluatorAndStopCache(root, HookHandlerPreToolUse, "claude-pre-tool-use", payload, evaluator, cache); result.ExitCode != 0 {
		t.Fatalf("warm worker pre-hook failed: %+v", result)
	}
	warm, ok := cache.verifiedEvidencePrefix(root.Path(), sessionID)
	if !ok || warm.count != prefix.count || len(warm.segments) != len(prefix.segments) {
		t.Fatalf("warm worker pre-hook changed the verified prefix unexpectedly: ok=%t prefix=%+v", ok, warm)
	}

	appendCommandValues(t, repo, sessionID, []string{"live-command"})
	appendCommandRange(t, repo, sessionID, 1, maxCommandEvidenceItems)
	state, err = LoadSessionState(repo, sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if state.EvidenceOverflow || state.EvidenceSegmentCount != 3 {
		t.Fatalf("append-only suffix did not produce a clean third segment: %+v", state)
	}
	if result := RunHookRequestWithEvaluatorAndStopCache(root, HookHandlerPreToolUse, "claude-pre-tool-use", payload, evaluator, cache); result.ExitCode != 0 {
		t.Fatalf("append-only worker pre-hook failed: %+v", result)
	}
	appended, ok := cache.verifiedEvidencePrefix(root.Path(), sessionID)
	if !ok || appended.count != 3 || len(appended.segments) != 3 {
		t.Fatalf("append-only worker pre-hook did not extend the verified prefix: ok=%t prefix=%+v", ok, appended)
	}

	const otherSessionID = "worker-prefix-other"
	otherState := writeEvidenceChainFixture(t, repo, otherSessionID, 1, 128)
	if err := SaveSessionState(otherState); err != nil {
		t.Fatal(err)
	}
	otherPayload := []byte(`{"session_id":"worker-prefix-other","tool_use_id":"other-call","tool_name":"Write","tool_input":{"file_path":"src/a.go"}}`)
	if result := RunHookRequestWithEvaluatorAndStopCache(root, HookHandlerPreToolUse, "claude-pre-tool-use", otherPayload, evaluator, cache); result.ExitCode != 0 {
		t.Fatalf("isolated worker pre-hook failed: %+v", result)
	}
	if other, ok := cache.verifiedEvidencePrefix(root.Path(), otherSessionID); !ok || other.count != 1 {
		t.Fatalf("other session prefix was not isolated: ok=%t prefix=%+v", ok, other)
	}

	path := evidenceSegmentPath(root.Path(), sessionID, 1)
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	body[len(body)/2] ^= 1
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}
	corrupt := RunHookRequestWithEvaluatorAndStopCache(root, HookHandlerPreToolUse, "claude-pre-tool-use", payload, evaluator, cache)
	if corrupt.ExitCode != 2 {
		t.Fatalf("corrupt worker prefix was not rejected: %+v", corrupt)
	}
	if _, ok := cache.verifiedEvidencePrefix(root.Path(), sessionID); ok {
		t.Fatal("corrupt worker prefix remained cached")
	}
	if _, ok := cache.verifiedEvidencePrefix(root.Path(), otherSessionID); !ok {
		t.Fatal("corrupting one session evicted another session's verified prefix")
	}
}

func TestPreDecisionCacheBindsReachedEvidenceContentWithEqualMetadata(t *testing.T) {
	repo, root, payload := setupPreDecisionCommandDependencyRepo(t, `rules:
  - id: evidence-command
    kind: any_of
    when_paths: ['src/**']
    checks:
      - kind: forbid_command
        commands: ['danger']
      - kind: require_evidence
        file: 'proof.txt'
        must_contain: ['allow']
    mode: block
    message: evidence gate
`)
	path := filepath.Join(repo, "proof.txt")
	writePreDecisionDependencyFile(t, path, []byte("allow\n"), 0o644)
	if result := RunHookRequest(root, HookHandlerPreToolUse, "claude-pre-tool-use", payload); result.ExitCode != 0 || result.decisionClass != preDecisionResultPass {
		t.Fatalf("initial evidence decision = %+v class=%q", result, result.decisionClass)
	}
	assertPreDecisionCacheExists(t, root.Path(), payload)
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	writePreDecisionDependencyFile(t, path, []byte("block\n"), 0o644)
	if err := os.Chtimes(path, info.ModTime(), info.ModTime()); err != nil {
		t.Fatal(err)
	}
	result := RunHookRequest(root, HookHandlerPreToolUse, "claude-pre-tool-use", payload)
	if result.ExitCode != 2 || !strings.Contains(result.Stderr, "evidence-command") {
		t.Fatalf("equal-metadata evidence mutation reused stale pass: %+v", result)
	}
}

func TestPreDecisionCacheExpiresReachedFreshFile(t *testing.T) {
	repo, root, payload := setupPreDecisionCommandDependencyRepo(t, `rules:
  - id: freshness-command
    kind: any_of
    when_paths: ['src/**']
    checks:
      - kind: forbid_command
        commands: ['danger']
      - kind: require_fresh_file
        path: 'status.txt'
        max_age_hours: 1
    mode: block
    message: freshness gate
`)
	path := filepath.Join(repo, "status.txt")
	writePreDecisionDependencyFile(t, path, []byte("current\n"), 0o644)
	if result := RunHookRequest(root, HookHandlerPreToolUse, "claude-pre-tool-use", payload); result.ExitCode != 0 {
		t.Fatalf("initial freshness decision = %+v", result)
	}
	assertPreDecisionCacheExists(t, root.Path(), payload)
	old := time.Now().Add(-2 * time.Hour)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}
	result := RunHookRequest(root, HookHandlerPreToolUse, "claude-pre-tool-use", payload)
	if result.ExitCode != 2 || !strings.Contains(result.Stderr, "freshness-command") {
		t.Fatalf("expired fresh-file dependency reused stale pass: %+v", result)
	}
}

func TestPreDecisionCacheBindsReachedScriptAndDeclaredInput(t *testing.T) {
	repo, root, payload := setupPreDecisionCommandDependencyRepo(t, `rules:
  - id: script-command
    kind: any_of
    when_paths: ['src/**']
    checks:
      - kind: forbid_command
        commands: ['danger']
      - kind: require_script
        script: 'scripts/check.sh'
        cache_inputs: ['build/gate.txt']
    mode: block
    message: script gate
`)
	writePreDecisionDependencyFile(t, filepath.Join(repo, "scripts", "check.sh"), []byte("#!/bin/sh\nif grep -qx allow build/gate.txt; then exit 0; fi\nexit 2\n"), 0o755)
	input := filepath.Join(repo, "build", "gate.txt")
	writePreDecisionDependencyFile(t, input, []byte("allow\n"), 0o644)
	if result := RunHookRequest(root, HookHandlerPreToolUse, "claude-pre-tool-use", payload); result.ExitCode != 0 {
		t.Fatalf("initial script decision = %+v", result)
	}
	assertPreDecisionCacheExists(t, root.Path(), payload)
	info, err := os.Stat(input)
	if err != nil {
		t.Fatal(err)
	}
	writePreDecisionDependencyFile(t, input, []byte("block\n"), 0o644)
	if err := os.Chtimes(input, info.ModTime(), info.ModTime()); err != nil {
		t.Fatal(err)
	}
	result := RunHookRequest(root, HookHandlerPreToolUse, "claude-pre-tool-use", payload)
	if result.ExitCode != 2 || !strings.Contains(result.Stderr, "script-command") {
		t.Fatalf("declared script-input mutation reused stale pass: %+v", result)
	}
}

func TestPreDecisionCacheBindsReachedScriptContent(t *testing.T) {
	repo, root, payload := setupPreDecisionCommandDependencyRepo(t, `rules:
  - id: script-content
    kind: any_of
    when_paths: ['src/**']
    checks:
      - kind: forbid_command
        commands: ['danger']
      - kind: require_script
        script: 'scripts/check.sh'
        cache_inputs: ['build/gate.txt']
    mode: block
    message: script content gate
`)
	script := filepath.Join(repo, "scripts", "check.sh")
	writePreDecisionDependencyFile(t, filepath.Join(repo, "build", "gate.txt"), []byte("stable\n"), 0o644)
	writePreDecisionDependencyFile(t, script, []byte("#!/bin/sh\nexit 0\n"), 0o755)
	if result := RunHookRequest(root, HookHandlerPreToolUse, "claude-pre-tool-use", payload); result.ExitCode != 0 {
		t.Fatalf("initial script decision = %+v", result)
	}
	assertPreDecisionCacheExists(t, root.Path(), payload)
	writePreDecisionDependencyFile(t, script, []byte("#!/bin/sh\nexit 2\n"), 0o755)
	result := RunHookRequest(root, HookHandlerPreToolUse, "claude-pre-tool-use", payload)
	if result.ExitCode != 2 || !strings.Contains(result.Stderr, "script-content") {
		t.Fatalf("changed script content reused stale pass: %+v", result)
	}
}

func TestPreDecisionCacheBindsReachedScriptEnvironment(t *testing.T) {
	t.Setenv("LANG", "reconc-pass")
	repo, root, payload := setupPreDecisionCommandDependencyRepo(t, `rules:
  - id: script-environment
    kind: any_of
    when_paths: ['src/**']
    checks:
      - kind: forbid_command
        commands: ['danger']
      - kind: require_script
        script: 'scripts/check.sh'
        cache_inputs: ['build/gate.txt']
    mode: block
    message: script environment gate
`)
	writePreDecisionDependencyFile(t, filepath.Join(repo, "scripts", "check.sh"), []byte("#!/bin/sh\nif [ \"$LANG\" = reconc-pass ]; then exit 0; fi\nexit 2\n"), 0o755)
	writePreDecisionDependencyFile(t, filepath.Join(repo, "build", "gate.txt"), []byte("stable\n"), 0o644)
	if result := RunHookRequest(root, HookHandlerPreToolUse, "claude-pre-tool-use", payload); result.ExitCode != 0 {
		t.Fatalf("initial script environment decision = %+v", result)
	}
	assertPreDecisionCacheExists(t, root.Path(), payload)
	t.Setenv("LANG", "reconc-block")
	result := RunHookRequest(root, HookHandlerPreToolUse, "claude-pre-tool-use", payload)
	if result.ExitCode != 2 || !strings.Contains(result.Stderr, "script-environment") {
		t.Fatalf("changed script environment reused stale pass: %+v", result)
	}
}

func TestPreDecisionOperationalCompositeFailureNeverWarmsPassingDecision(t *testing.T) {
	repo, root, payload := setupPreDecisionCommandDependencyRepo(t, `rules:
  - id: script-error-with-fallback
    kind: any_of
    when_paths: ['src/**']
    checks:
      - kind: forbid_command
        commands: ['danger']
      - kind: require_script
        script: 'scripts/check.sh'
        cache_inputs: ['build/gate.txt']
      - kind: require_evidence
        file: 'proof.txt'
        must_exist: true
    mode: block
    message: script fallback gate
`)
	writePreDecisionDependencyFile(t, filepath.Join(repo, "scripts", "check.sh"), []byte("#!/bin/sh\nexit 1\n"), 0o755)
	writePreDecisionDependencyFile(t, filepath.Join(repo, "build", "gate.txt"), []byte("stable\n"), 0o644)
	writePreDecisionDependencyFile(t, filepath.Join(repo, "proof.txt"), []byte("present\n"), 0o644)
	result := RunHookRequest(root, HookHandlerPreToolUse, "claude-pre-tool-use", payload)
	if result.ExitCode != 0 || result.decisionClass != "" {
		t.Fatalf("fallback decision classification = %+v, want uncacheable pass", result)
	}
	if _, err := os.Stat(preDecisionCachePath(root.Path(), payload)); !os.IsNotExist(err) {
		t.Fatalf("passing decision with an operational sub-check failure warmed cache: %v", err)
	}
}

func TestPreDecisionWarningNeverWarmsPassCache(t *testing.T) {
	_, root, payload := setupPreDecisionCommandDependencyRepo(t, `rules:
  - id: warning-command
    kind: forbid_command
    commands: ['danger']
    mode: warn
    message: command warning
`)
	result := RunHookRequest(root, HookHandlerPreToolUse, "claude-pre-tool-use", payload)
	if result.ExitCode != 0 || result.decisionClass != "" {
		t.Fatalf("warning decision classification = %+v, want uncacheable pass", result)
	}
	if _, err := os.Stat(preDecisionCachePath(root.Path(), payload)); !os.IsNotExist(err) {
		t.Fatalf("warning decision warmed pass cache: %v", err)
	}
}

func TestPreDecisionFreshnessStateChangesAtDeadline(t *testing.T) {
	modified := time.Unix(1_000, 0)
	before := preDecisionPathObservation{Exists: true, ResolvedModTime: modified.UnixNano()}
	beforeDeadline := applyPreDecisionFreshness(&before, []int{1}, modified.Add(time.Hour-time.Nanosecond))
	if len(before.Freshness) != 1 || before.Freshness[0].Expired || !beforeDeadline.Equal(modified.Add(time.Hour)) {
		t.Fatalf("pre-deadline freshness = %+v deadline=%s", before.Freshness, beforeDeadline)
	}
	after := preDecisionPathObservation{Exists: true, ResolvedModTime: modified.UnixNano()}
	afterDeadline := applyPreDecisionFreshness(&after, []int{1}, modified.Add(time.Hour+time.Nanosecond))
	if len(after.Freshness) != 1 || !after.Freshness[0].Expired || !afterDeadline.IsZero() {
		t.Fatalf("post-deadline freshness = %+v deadline=%s", after.Freshness, afterDeadline)
	}
}

func TestPreDecisionOperationalScriptFailureNeverWarmsCache(t *testing.T) {
	repo, root, payload := setupPreDecisionCommandDependencyRepo(t, `rules:
  - id: script-error
    kind: any_of
    when_paths: ['src/**']
    checks:
      - kind: forbid_command
        commands: ['danger']
      - kind: require_script
        script: 'scripts/check.sh'
        cache_inputs: ['build/gate.txt']
    mode: block
    message: script gate
`)
	script := filepath.Join(repo, "scripts", "check.sh")
	writePreDecisionDependencyFile(t, filepath.Join(repo, "build", "gate.txt"), []byte("stable\n"), 0o644)
	writePreDecisionDependencyFile(t, script, []byte("#!/bin/sh\nexit 1\n"), 0o755)
	result := RunHookRequest(root, HookHandlerPreToolUse, "claude-pre-tool-use", payload)
	if result.ExitCode != 2 || !strings.Contains(result.Stderr, "script-error") {
		t.Fatalf("operational script failure did not fail closed: %+v", result)
	}
	if _, err := os.Stat(preDecisionCachePath(root.Path(), payload)); !os.IsNotExist(err) {
		t.Fatalf("operational script failure warmed cache: %v", err)
	}
	writePreDecisionDependencyFile(t, script, []byte("#!/bin/sh\nexit 0\n"), 0o755)
	if healthy := RunHookRequest(root, HookHandlerPreToolUse, "claude-pre-tool-use", payload); healthy.ExitCode != 0 {
		t.Fatalf("healthy retry did not evaluate live: %+v", healthy)
	}
	assertPreDecisionCacheExists(t, root.Path(), payload)
}

func TestPreDecisionDependencyMutationDuringEvaluationPreventsPublication(t *testing.T) {
	repo, root, payload := setupPreDecisionCommandDependencyRepo(t, `rules:
  - id: script-mutates-input
    kind: any_of
    when_paths: ['src/**']
    checks:
      - kind: forbid_command
        commands: ['danger']
      - kind: require_script
        script: 'scripts/check.sh'
        cache_inputs: ['build/gate.txt']
    mode: block
    message: script mutation gate
`)
	writePreDecisionDependencyFile(t, filepath.Join(repo, "scripts", "check.sh"), []byte("#!/bin/sh\nprintf changed > build/gate.txt\nexit 0\n"), 0o755)
	writePreDecisionDependencyFile(t, filepath.Join(repo, "build", "gate.txt"), []byte("initial\n"), 0o644)
	result := RunHookRequest(root, HookHandlerPreToolUse, "claude-pre-tool-use", payload)
	if result.ExitCode != 0 {
		t.Fatalf("live script decision = %+v", result)
	}
	if _, err := os.Stat(preDecisionCachePath(root.Path(), payload)); !os.IsNotExist(err) {
		t.Fatalf("dependency mutation during evaluation warmed cache: %v", err)
	}
}

func TestPreDecisionCacheWriterRequiresTypedPolicyDecision(t *testing.T) {
	repo := setupPolicyRepo(t)
	payload := &HookPayload{SessionID: "typed", ToolUseID: "call", ToolName: "Write", ToolInput: map[string]interface{}{"file_path": "src/main.go"}}
	if _, err := InitializeSessionState(repo, payload.SessionID); err != nil {
		t.Fatal(err)
	}
	inputs, ok := preDecisionInputsForPayload(repo, payload)
	if !ok {
		t.Fatal("pre-decision inputs are unexpectedly uncacheable")
	}
	for _, result := range []Result{
		{ExitCode: 0},
		{ExitCode: 2, Stderr: "operational failure"},
		{ExitCode: 0, decisionClass: preDecisionResultBlock},
		{ExitCode: 2, decisionClass: preDecisionResultPass},
		{ExitCode: 0, Err: errors.New("internal failure"), decisionClass: preDecisionResultPass},
	} {
		if err := writePreDecisionCacheForPayload(repo, payload, inputs.key, result); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(preDecisionCachePathForPayload(repo, payload)); !os.IsNotExist(err) {
			t.Fatalf("untyped or contradictory result was cached: %+v err=%v", result, err)
		}
	}
}

func TestPreDecisionCacheReaderRejectsInvalidDecisionClass(t *testing.T) {
	repo := setupPolicyRepo(t)
	payload := &HookPayload{SessionID: "corrupt", ToolUseID: "call", ToolName: "Write", ToolInput: map[string]interface{}{"file_path": "src/main.go"}}
	if _, err := InitializeSessionState(repo, payload.SessionID); err != nil {
		t.Fatal(err)
	}
	inputs, ok := preDecisionInputsForPayload(repo, payload)
	if !ok {
		t.Fatal("pre-decision inputs are unexpectedly uncacheable")
	}
	path := preDecisionCachePathForPayload(repo, payload)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	body := []byte(`{"format_version":"` + preDecisionCacheVersion + `","key":"` + inputs.key + `","decision_class":"operational","exit_code":0}`)
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}
	if cached, ok := readPreDecisionCacheCandidate(repo, payload); ok {
		t.Fatalf("invalid decision class was accepted: %+v", cached)
	}
}

func setupPreDecisionCommandDependencyRepo(t *testing.T, policy string) (string, ResolvedRepoRoot, []byte) {
	t.Helper()
	repo := setupPolicyRepo(t)
	gitInitHelper(t, repo)
	writePreDecisionDependencyFile(t, filepath.Join(repo, "policies", "rules.yml"), []byte(policy), 0o644)
	if _, err := compiler.CompileRepoPolicy(repo, "test"); err != nil {
		t.Fatalf("compile dependency policy: %v", err)
	}
	if _, err := InitializeSessionState(repo, "dependency"); err != nil {
		t.Fatal(err)
	}
	if _, err := MutateSessionState(repo, "dependency", func(state SessionState) SessionState {
		return AppendWritePath(state, "src/main.go")
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := EnsureSessionState(repo, "dependency"); err != nil {
		t.Fatal(err)
	}
	root, err := ResolveRepoRootRef(repo)
	if err != nil {
		t.Fatal(err)
	}
	payload := []byte(`{"session_id":"dependency","tool_use_id":"same-call","tool_name":"Bash","tool_input":{"command":"danger"}}`)
	return repo, root, payload
}

func writePreDecisionDependencyFile(t *testing.T, path string, body []byte, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, body, mode); err != nil {
		t.Fatal(err)
	}
}

func assertPreDecisionCacheExists(t *testing.T, root string, payload []byte) {
	t.Helper()
	if _, err := os.Stat(preDecisionCachePath(root, payload)); err != nil {
		t.Fatalf("pre-decision cache was not written: %v", err)
	}
}
