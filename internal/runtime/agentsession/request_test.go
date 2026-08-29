package agentsession

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestResolvedHookRequestOwnsRootAndRuntime(t *testing.T) {
	repo := setupPolicyRepo(t)
	root, err := ResolveRepoRootRef(repo)
	if err != nil {
		t.Fatal(err)
	}
	result := RunHookRequest(root, HookHandlerSessionStart, "omp-session-start", []byte(`{"session_id":"request-runtime"}`))
	if result.ExitCode != 0 {
		t.Fatalf("session start failed: %+v", result)
	}
	state, err := loadSessionStateWithLockResolved(root.Path(), "request-runtime")
	if err != nil {
		t.Fatal(err)
	}
	if state.Runtime != "omp" {
		t.Fatalf("runtime=%q, want omp", state.Runtime)
	}
}

func TestPassiveHookRequestDoesNotCreateSessionState(t *testing.T) {
	repo := setupPolicyRepo(t)
	root, err := ResolveRepoRootRef(repo)
	if err != nil {
		t.Fatal(err)
	}
	result := RunHookRequest(root, HookHandlerPassive, "grok-notification", []byte(`{"session_id":"passive-only"}`))
	if result.ExitCode != 0 || result.Stderr != "" {
		t.Fatalf("passive result: %+v", result)
	}
	for _, path := range []string{
		sessionStatePath(root.Path(), "passive-only"),
		activeSessionPath(root.Path()),
	} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("passive request created %s: %v", path, err)
		}
	}
}

func TestPreDecisionCacheRequiresExactToolPolicyAndEvidenceIdentity(t *testing.T) {
	repo := setupPolicyRepo(t)
	root, err := ResolveRepoRootRef(repo)
	if err != nil {
		t.Fatal(err)
	}
	if result := RunHookRequest(root, HookHandlerSessionStart, "claude-session-start", []byte(`{"session_id":"decision"}`)); result.ExitCode != 0 {
		t.Fatalf("session start: %+v", result)
	}
	allowed := []byte(`{"session_id":"decision","tool_use_id":"call-1","tool_name":"Write","tool_input":{"file_path":"src/main.go"}}`)
	if result := RunHookRequest(root, HookHandlerPreToolUse, "claude-pre-tool-use", allowed); result.ExitCode != 0 {
		t.Fatalf("initial decision: %+v", result)
	}
	cachePath := preDecisionCachePath(root.Path(), allowed)
	if _, err := os.Stat(cachePath); err != nil {
		t.Fatalf("cache not written: %v", err)
	}
	body, err := os.ReadFile(cachePath)
	if err != nil {
		t.Fatal(err)
	}
	var cached preDecisionCache
	if err := json.Unmarshal(body, &cached); err != nil {
		t.Fatal(err)
	}
	permission := RunHookRequest(root, HookHandlerPermissionRequest, "claude-permission-request", allowed)
	if permission.ExitCode != 0 || permission.Stdout != "" || permission.Stderr != "" {
		t.Fatalf("identical permission decision changed: %+v", permission)
	}

	if _, err := mutateSessionStateResolved(root.Path(), "decision", func(state SessionState) SessionState {
		return AppendReadPath(state, "docs/documentation.md")
	}); err != nil {
		t.Fatal(err)
	}
	keyAfterEvidence, ok := preDecisionKey(root.Path(), allowed)
	if !ok {
		t.Fatal("expected cacheable key after evidence mutation")
	}
	if cached.Key == keyAfterEvidence {
		t.Fatal("evidence mutation did not invalidate decision identity")
	}

	changedInput := []byte(`{"session_id":"decision","tool_use_id":"call-1","tool_name":"Write","tool_input":{"file_path":"generated/main.go"}}`)
	denied := RunHookRequest(root, HookHandlerPermissionRequest, "claude-permission-request", changedInput)
	if denied.ExitCode != 0 || !strings.Contains(denied.Stdout, `"behavior":"deny"`) {
		t.Fatalf("changed input reused stale allow: %+v", denied)
	}

	lockPath := filepath.Join(root.Path(), ".reconc", "policy.lock.json")
	lockBody, err := os.ReadFile(lockPath)
	if err != nil {
		t.Fatal(err)
	}
	lockBody = append(lockBody, '\n')
	if err := os.WriteFile(lockPath, lockBody, 0o644); err != nil {
		t.Fatal(err)
	}
	policyResult := RunHookRequest(root, HookHandlerPreToolUse, "claude-pre-tool-use", allowed)
	if policyResult.ExitCode != 0 {
		t.Fatalf("semantically unchanged policy should still allow: %+v", policyResult)
	}
	updatedBody, err := os.ReadFile(cachePath)
	if err != nil {
		t.Fatal(err)
	}
	var updatedCache preDecisionCache
	if err := json.Unmarshal(updatedBody, &updatedCache); err != nil {
		t.Fatal(err)
	}
	if updatedCache.Key == cached.Key {
		t.Fatal("policy byte mutation reused the previous decision identity")
	}

	sourcePath := filepath.Join(root.Path(), "policies", "rules.yml")
	sourceBody, err := os.ReadFile(sourcePath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(sourcePath, append(sourceBody, []byte("# changed\n")...), 0o644); err != nil {
		t.Fatal(err)
	}
	staleResult := RunHookRequest(root, HookHandlerPreToolUse, "claude-pre-tool-use", allowed)
	if staleResult.ExitCode != 2 || !strings.Contains(staleResult.Stderr, "refresh") {
		t.Fatalf("policy source mutation reused a stale decision: %+v", staleResult)
	}
}

func TestPreDecisionWithoutStableToolIdentityIsNotCached(t *testing.T) {
	repo := setupPolicyRepo(t)
	root, err := ResolveRepoRootRef(repo)
	if err != nil {
		t.Fatal(err)
	}
	if result := RunHookRequest(root, HookHandlerSessionStart, "claude-session-start", []byte(`{"session_id":"no-tool-id"}`)); result.ExitCode != 0 {
		t.Fatal(result.Stderr)
	}
	payload := []byte(`{"session_id":"no-tool-id","tool_name":"Write","tool_input":{"file_path":"src/main.go"}}`)
	if result := RunHookRequest(root, HookHandlerPreToolUse, "claude-pre-tool-use", payload); result.ExitCode != 0 {
		t.Fatalf("pre decision failed: %+v", result)
	}
	path := filepath.Join(projectDir(root.Path()), "pre-decisions", sessionFileKey("no-tool-id")+".json")
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("unstable decision was cached: %v", err)
	}
}

func TestPreDecisionIdentityResamplingReusesPayloadAndDetectsEvidenceMutation(t *testing.T) {
	repo := setupPolicyRepo(t)
	root, err := ResolveRepoRootRef(repo)
	if err != nil {
		t.Fatal(err)
	}
	if result := RunHookRequest(root, HookHandlerSessionStart, "claude-session-start", []byte(`{"session_id":"identity-resample"}`)); result.ExitCode != 0 {
		t.Fatalf("session start: %+v", result)
	}
	payload := &HookPayload{
		SessionID: "identity-resample", ToolUseID: "call-identity", ToolName: "Write",
		ToolInput: map[string]interface{}{"file_path": "src/main.go"},
	}
	initial, ok := preDecisionInputsForPayload(root.Path(), payload)
	if !ok {
		t.Fatal("initial pre-decision identity was not cacheable")
	}
	resampled, ok := resamplePreDecisionInputs(root.Path(), payload, initial)
	if !ok || !initial.identity.equal(resampled.identity) || initial.key != resampled.key {
		t.Fatalf("unchanged identity was not preserved: initial=%+v resampled=%+v cacheable=%v", initial, resampled, ok)
	}
	if _, err := mutateSessionStateResolved(root.Path(), payload.SessionID, func(state SessionState) SessionState {
		return AppendReadPath(state, "docs/documentation.md")
	}); err != nil {
		t.Fatal(err)
	}
	changed, ok := resamplePreDecisionInputs(root.Path(), payload, initial)
	if !ok {
		t.Fatal("mutated pre-decision identity became unexpectedly uncacheable")
	}
	if initial.identity.equal(changed.identity) || initial.key == changed.key {
		t.Fatalf("session evidence mutation did not change typed identity: initial=%+v changed=%+v", initial, changed)
	}
}

func TestPreDecisionIdentityResamplingIsConcurrentSafe(t *testing.T) {
	repo := setupPolicyRepo(t)
	root, err := ResolveRepoRootRef(repo)
	if err != nil {
		t.Fatal(err)
	}
	if result := RunHookRequest(root, HookHandlerSessionStart, "claude-session-start", []byte(`{"session_id":"identity-race"}`)); result.ExitCode != 0 {
		t.Fatalf("session start: %+v", result)
	}
	payload := &HookPayload{
		SessionID: "identity-race", ToolUseID: "call-identity", ToolName: "Write",
		ToolInput: map[string]interface{}{"file_path": "src/main.go"},
	}
	initial, ok := preDecisionInputsForPayload(root.Path(), payload)
	if !ok {
		t.Fatal("initial pre-decision identity was not cacheable")
	}
	const callers = 16
	var wait sync.WaitGroup
	wait.Add(callers)
	errors := make(chan error, callers)
	for range callers {
		go func() {
			defer wait.Done()
			resampled, cacheable := resamplePreDecisionInputs(root.Path(), payload, initial)
			if !cacheable || !initial.identity.equal(resampled.identity) {
				errors <- fmt.Errorf("concurrent identity resample changed: cacheable=%v identity=%+v", cacheable, resampled.identity)
			}
		}()
	}
	wait.Wait()
	close(errors)
	for err := range errors {
		t.Fatal(err)
	}
}

func TestPreDecisionCacheInvalidatesOnGitAliasMutation(t *testing.T) {
	repo := setupPolicyRepo(t)
	runGitGuardTestCommand(t, "-C", repo, "init", "--quiet")
	runGitGuardTestCommand(t, "-C", repo, "config", "alias.st", "status")
	root, err := ResolveRepoRootRef(repo)
	if err != nil {
		t.Fatal(err)
	}
	if result := RunHookRequest(root, HookHandlerSessionStart, "claude-session-start", []byte(`{"session_id":"alias-decision"}`)); result.ExitCode != 0 {
		t.Fatalf("session start: %+v", result)
	}
	payload := []byte(`{"session_id":"alias-decision","tool_use_id":"call-alias","tool_name":"Bash","tool_input":{"command":"git st"}}`)
	if result := RunHookRequest(root, HookHandlerPreToolUse, "claude-pre-tool-use", payload); result.ExitCode != 0 {
		t.Fatalf("safe alias decision: %+v", result)
	}
	before, ok := preDecisionKey(root.Path(), payload)
	if !ok {
		t.Fatal("safe alias decision was not cacheable")
	}
	runGitGuardTestCommand(t, "-C", repo, "config", "alias.st", "reset --hard")
	after, ok := preDecisionKey(root.Path(), payload)
	if !ok || before == after {
		t.Fatalf("alias mutation did not change cache identity: before=%q after=%q cacheable=%v", before, after, ok)
	}
	if result := RunHookRequest(root, HookHandlerPreToolUse, "claude-pre-tool-use", payload); result.ExitCode != 2 {
		t.Fatalf("destructive alias reused stale allow: %+v", result)
	}
	updatedBody, err := os.ReadFile(preDecisionCachePath(root.Path(), payload))
	if err != nil {
		t.Fatal(err)
	}
	var updated preDecisionCache
	if err := json.Unmarshal(updatedBody, &updated); err != nil {
		t.Fatal(err)
	}
	if updated.Key != after {
		t.Fatalf("post-mutation cache key = %q, want fresh alias identity %q", updated.Key, after)
	}
}

func BenchmarkResolvedHookEvents(b *testing.B) {
	repo := setupStopBenchmarkRepo(b)
	root, err := ResolveRepoRootRef(repo)
	if err != nil {
		b.Fatal(err)
	}
	start := []byte(`{"session_id":"bench-request"}`)
	if result := RunHookRequest(root, HookHandlerSessionStart, "claude-session-start", start); result.ExitCode != 0 {
		b.Fatal(result.Stderr)
	}
	pre := []byte(`{"session_id":"bench-request","tool_use_id":"bench-call","tool_name":"Write","tool_input":{"file_path":"src/a.go"}}`)
	post := []byte(`{"session_id":"bench-request","tool_use_id":"bench-call","tool_name":"Read","tool_input":{"file_path":"src/a.go"}}`)
	cases := []struct {
		name    string
		handler HookHandler
		payload []byte
	}{
		{name: "session-start", handler: HookHandlerSessionStart, payload: start},
		{name: "passive", handler: HookHandlerPassive, payload: start},
		{name: "pre-tool", handler: HookHandlerPreToolUse, payload: pre},
		{name: "permission", handler: HookHandlerPermissionRequest, payload: pre},
		{name: "post-tool", handler: HookHandlerPostToolUse, payload: post},
		{name: "compaction", handler: HookHandlerPostCompaction, payload: start},
		{name: "stop", handler: HookHandlerStop, payload: start},
		{name: "workspace", handler: HookHandlerWorkspaceOpen, payload: []byte(`{}`)},
	}
	for _, tc := range cases {
		b.Run(tc.name, func(b *testing.B) {
			b.ReportAllocs()
			for index := 0; index < b.N; index++ {
				_ = RunHookRequest(root, tc.handler, "claude-"+tc.name, tc.payload)
			}
		})
	}
}

func BenchmarkPreDecisionKeyPayloadDecode(b *testing.B) {
	repo := setupStopBenchmarkRepo(b)
	payloadBytes := []byte(`{"session_id":"bench-decode","tool_use_id":"call-1","tool_name":"Write","tool_input":{"file_path":"src/a.go"}}`)
	payload, err := ParsePayload(payloadBytes)
	if err != nil {
		b.Fatal(err)
	}
	initial, ok := preDecisionInputsForPayload(repo, payload)
	if !ok {
		b.Fatal("pre-decision identity is not cacheable")
	}
	b.Run("decode-each-key", func(b *testing.B) {
		b.ReportAllocs()
		for range b.N {
			if _, ok := preDecisionKey(repo, payloadBytes); !ok {
				b.Fatal("key is not cacheable")
			}
		}
	})
	b.Run("reuse-decoded-payload", func(b *testing.B) {
		b.ReportAllocs()
		for range b.N {
			if _, ok := preDecisionKeyForPayload(repo, payload); !ok {
				b.Fatal("key is not cacheable")
			}
		}
	})
	b.Run("resample-typed-components", func(b *testing.B) {
		b.ReportAllocs()
		for range b.N {
			resampled, ok := resamplePreDecisionInputs(repo, payload, initial)
			if !ok || !initial.identity.equal(resampled.identity) {
				b.Fatal("typed identity resampling changed without an input mutation")
			}
		}
	})
}
