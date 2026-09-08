package agentsession

import (
	"os"
	"path/filepath"
	"testing"
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
