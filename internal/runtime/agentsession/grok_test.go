package agentsession

import (
	"encoding/json"
	"strings"
	"testing"
)

type grokTestPayload struct {
	HookEventName      string            `json:"hookEventName"`
	SessionID          string            `json:"sessionId"`
	WorkspaceRoot      string            `json:"workspaceRoot"`
	ToolName           string            `json:"toolName,omitempty"`
	ToolUseID          string            `json:"toolUseId,omitempty"`
	ToolInput          map[string]string `json:"toolInput,omitempty"`
	ToolResult         string            `json:"toolResult,omitempty"`
	ToolInputTruncated bool              `json:"toolInputTruncated,omitempty"`
}

func marshalGrokTestPayload(t *testing.T, payload grokTestPayload) []byte {
	t.Helper()
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal Grok test payload: %v", err)
	}
	return body
}

func TestNormalizeGrokPayloadPreToolUse(t *testing.T) {
	repo := t.TempDir()
	t.Setenv("GROK_SESSION_ID", "grok-session")
	t.Setenv("GROK_WORKSPACE_ROOT", repo)
	body, err := NormalizeGrokPayload("grok-pre-tool-use", marshalGrokTestPayload(t, grokTestPayload{
		HookEventName: "pre_tool_use",
		SessionID:     "grok-session",
		WorkspaceRoot: repo,
		ToolName:      "search_replace",
		ToolUseID:     "call-1",
		ToolInput: map[string]string{
			"path":       "src/app.go",
			"old_string": "a",
			"new_string": "b",
		},
	}), repo)
	if err != nil {
		t.Fatal(err)
	}
	payload, err := ParsePayload(body)
	if err != nil {
		t.Fatal(err)
	}
	if payload.SessionID != "grok-session" || payload.ToolName != "Edit" ||
		payload.ToolUseID != "call-1" || payload.FilePath() != "src/app.go" {
		t.Fatalf("unexpected normalized Grok payload: %+v raw=%v", payload, payload.Raw)
	}
	if payload.Raw["reconc_runtime"] != "grok" {
		t.Fatalf("runtime marker missing: %#v", payload.Raw)
	}
}

func TestNormalizeGrokToolCoverage(t *testing.T) {
	tests := []struct {
		name string
		want string
	}{
		{name: "read_file", want: "Read"},
		{name: "hashline_read", want: "Read"},
		{name: "grep", want: "Read"},
		{name: "hashline_grep", want: "Read"},
		{name: "list_dir", want: "Read"},
		{name: "write", want: "Write"},
		{name: "search_replace", want: "Edit"},
		{name: "hashline_edit", want: "Edit"},
		{name: "run_terminal_command", want: "Bash"},
		{name: "run_terminal_cmd", want: "Bash"},
		{name: "linear__save_issue", want: "linear__save_issue"},
	}
	repo := t.TempDir()
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			body, err := NormalizeGrokPayload("grok-post-tool-use", marshalGrokTestPayload(t, grokTestPayload{
				HookEventName: "post_tool_use",
				SessionID:     "s1",
				WorkspaceRoot: repo,
				ToolName:      test.name,
				ToolInput:     map[string]string{},
				ToolResult:    "ok",
			}), repo)
			if err != nil {
				t.Fatal(err)
			}
			payload, err := ParsePayload(body)
			if err != nil {
				t.Fatal(err)
			}
			if payload.ToolName != test.want {
				t.Fatalf("tool %s normalized to %s, want %s", test.name, payload.ToolName, test.want)
			}
			if payload.ToolResponse["value"] != "ok" {
				t.Fatalf("scalar tool result was lost: %#v", payload.ToolResponse)
			}
		})
	}
}

func TestNormalizeGrokPayloadRejectsIdentityDriftAndTruncation(t *testing.T) {
	repo := t.TempDir()
	other := t.TempDir()
	tests := []struct {
		name    string
		event   string
		payload grokTestPayload
		envID   string
		want    string
	}{
		{name: "event", event: "grok-pre-tool-use", payload: grokTestPayload{HookEventName: "post_tool_use", SessionID: "s", WorkspaceRoot: repo}, want: "does not match route"},
		{name: "workspace", event: "grok-pre-tool-use", payload: grokTestPayload{HookEventName: "pre_tool_use", SessionID: "s", WorkspaceRoot: other}, want: "does not match repository root"},
		{name: "session", event: "grok-pre-tool-use", payload: grokTestPayload{HookEventName: "pre_tool_use", SessionID: "s", WorkspaceRoot: repo}, envID: "other", want: "does not match GROK_SESSION_ID"},
		{name: "truncated", event: "grok-pre-tool-use", payload: grokTestPayload{HookEventName: "pre_tool_use", SessionID: "s", WorkspaceRoot: repo, ToolInputTruncated: true}, want: "truncated"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("GROK_SESSION_ID", test.envID)
			_, err := NormalizeGrokPayload(test.event, marshalGrokTestPayload(t, test.payload), repo)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestPayloadLooksLikeGrok(t *testing.T) {
	if !PayloadLooksLikeGrok([]byte(`{"hookEventName":"pre_tool_use","sessionId":"s","workspaceRoot":"/repo"}`)) {
		t.Fatal("native Grok envelope was not detected")
	}
	if PayloadLooksLikeGrok([]byte(`{"hook_event_name":"PreToolUse","session_id":"s","workspace_root":"/repo"}`)) {
		t.Fatal("Claude-shaped payload was misdetected as native Grok")
	}
}

func TestAdaptGrokResultUsesExplicitDecisionJSON(t *testing.T) {
	denied := AdaptGrokResult("grok-pre-tool-use", Result{ExitCode: 2, Stderr: "blocked"})
	if denied.ExitCode != 0 || denied.Stderr != "" {
		t.Fatalf("Grok deny transport = %+v", denied)
	}
	var body map[string]string
	if err := json.Unmarshal([]byte(denied.Stdout), &body); err != nil {
		t.Fatal(err)
	}
	if body["decision"] != "deny" || body["reason"] != "blocked" {
		t.Fatalf("Grok deny payload = %#v", body)
	}

	allowed := AdaptGrokResult("grok-pre-tool-use", Result{})
	if allowed.ExitCode != 0 || allowed.Stdout != `{"decision":"allow"}` {
		t.Fatalf("Grok allow transport = %+v", allowed)
	}
}

func TestAdaptGrokStopIsPassiveButVisible(t *testing.T) {
	result := AdaptGrokResult("grok-stop", Result{
		Stdout: `{"decision":"block","reason":"finish the task"}`,
	})
	if result.ExitCode != 0 || result.Stdout != "" ||
		!strings.Contains(result.Stderr, "finish the task") {
		t.Fatalf("Grok passive Stop adaptation = %+v", result)
	}
}
