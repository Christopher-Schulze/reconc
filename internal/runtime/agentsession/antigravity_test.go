package agentsession

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestNormalizeAntigravityPayloadPreToolUseWrite(t *testing.T) {
	body, err := NormalizeAntigravityPayload("antigravity-pre-tool-use", []byte(`{
		"conversationId":"ag-1",
		"stepIdx":7,
		"toolCall":{"name":"replace_file_content","args":{"TargetFile":"src/app.go"}}
	}`))
	if err != nil {
		t.Fatalf("NormalizeAntigravityPayload: %v", err)
	}
	payload, err := ParsePayload(body)
	if err != nil {
		t.Fatalf("ParsePayload: %v", err)
	}
	if payload.SessionID != "ag-1" || payload.ToolUseID != "step:7" {
		t.Fatalf("unexpected identity: session=%q tool=%q", payload.SessionID, payload.ToolUseID)
	}
	if !payload.IsWriteTool() || payload.FilePath() != "src/app.go" {
		t.Fatalf("expected write to src/app.go, got name=%q path=%q", payload.ToolName, payload.FilePath())
	}
}

func TestNormalizeAntigravityPayloadRunCommand(t *testing.T) {
	body, err := NormalizeAntigravityPayload("antigravity-pre-tool-use", []byte(`{
		"conversationId":"ag-2",
		"stepIdx":8,
		"toolCall":{"name":"run_command","args":{"CommandLine":"go test ./...","Cwd":"/repo"}}
	}`))
	if err != nil {
		t.Fatalf("NormalizeAntigravityPayload: %v", err)
	}
	payload, err := ParsePayload(body)
	if err != nil {
		t.Fatalf("ParsePayload: %v", err)
	}
	if !payload.IsCommandTool() || payload.Command() != "go test ./..." {
		t.Fatalf("expected command tool, got name=%q command=%q", payload.ToolName, payload.Command())
	}
}

func TestAntigravityPrePostToolRecordsPendingEvidence(t *testing.T) {
	repo := setupPolicyRepo(t)
	pre := RunAntigravityPreToolUse(repo, []byte(`{
		"conversationId":"ag-3",
		"stepIdx":9,
		"toolCall":{"name":"view_file","args":{"AbsolutePath":"docs/tasks.md"}}
	}`))
	if pre.ExitCode != 0 || !strings.Contains(pre.Stdout, `"decision":"allow"`) {
		t.Fatalf("pre should allow, exit=%d stdout=%s stderr=%s", pre.ExitCode, pre.Stdout, pre.Stderr)
	}
	post := RunAntigravityPostToolUse(repo, []byte(`{
		"conversationId":"ag-3",
		"stepIdx":9,
		"error":""
	}`))
	if post.ExitCode != 0 || strings.TrimSpace(post.Stdout) != "{}" {
		t.Fatalf("post should return empty object, exit=%d stdout=%s stderr=%s", post.ExitCode, post.Stdout, post.Stderr)
	}
	state, err := LoadSessionState(repo, "ag-3")
	if err != nil {
		t.Fatalf("LoadSessionState: %v", err)
	}
	if len(state.ReadPaths) != 1 || state.ReadPaths[0] != "docs/tasks.md" {
		t.Fatalf("expected read evidence, got %+v", state.ReadPaths)
	}
	if len(state.PendingToolCalls) != 0 {
		t.Fatalf("pending tool call was not cleared: %+v", state.PendingToolCalls)
	}
}

func TestAntigravityPendingKeyWithoutStepIDUsesToolFingerprint(t *testing.T) {
	readDoc, err := NormalizeAntigravityPayload("antigravity-pre-tool-use", []byte(`{
		"conversationId":"ag-no-step",
		"toolCall":{"name":"view_file","args":{"AbsolutePath":"docs/tasks.md"}}
	}`))
	if err != nil {
		t.Fatalf("normalize docs read: %v", err)
	}
	readStart, err := NormalizeAntigravityPayload("antigravity-pre-tool-use", []byte(`{
		"conversationId":"ag-no-step",
		"toolCall":{"name":"view_file","args":{"AbsolutePath":"start.md"}}
	}`))
	if err != nil {
		t.Fatalf("normalize start read: %v", err)
	}
	docPayload, err := ParsePayload(readDoc)
	if err != nil {
		t.Fatalf("parse docs read: %v", err)
	}
	startPayload, err := ParsePayload(readStart)
	if err != nil {
		t.Fatalf("parse start read: %v", err)
	}
	docKey := antigravityPendingKey(docPayload)
	startKey := antigravityPendingKey(startPayload)
	if docKey == "step:unknown" || startKey == "step:unknown" {
		t.Fatalf("missing step id must still produce deterministic tool fingerprint keys, got %q and %q", docKey, startKey)
	}
	if docKey == startKey {
		t.Fatalf("different no-step tool calls must not collide, got %q", docKey)
	}
}

func TestAntigravityStopAdaptsBlockToContinue(t *testing.T) {
	result := AdaptAntigravityResult("antigravity-stop", Result{ExitCode: 0, Stdout: `{"decision":"block","reason":"fix it"}`})
	if result.ExitCode != 0 {
		t.Fatalf("adapt exit=%d", result.ExitCode)
	}
	var out map[string]string
	if err := json.Unmarshal([]byte(result.Stdout), &out); err != nil {
		t.Fatalf("stdout json: %v", err)
	}
	if out["decision"] != "continue" || out["reason"] != "fix it" {
		t.Fatalf("unexpected stop adaptation: %#v", out)
	}
}
