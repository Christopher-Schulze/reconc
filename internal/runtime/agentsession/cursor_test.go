package agentsession

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestNormalizeCursorPayloadPreToolUseWrite(t *testing.T) {
	body, err := NormalizeCursorPayload("cursor-pre-tool-use", []byte(`{
		"conversation_id":"cursor-1",
		"tool_name":"StrReplace",
		"tool_input":{"filePath":"generated/x.go"}
	}`))
	if err != nil {
		t.Fatalf("NormalizeCursorPayload: %v", err)
	}
	payload, err := ParsePayload(body)
	if err != nil {
		t.Fatalf("ParsePayload: %v", err)
	}
	if payload.SessionID != "cursor-1" {
		t.Fatalf("session id = %q", payload.SessionID)
	}
	if !payload.IsWriteTool() {
		t.Fatalf("expected write tool, got %q", payload.ToolName)
	}
	if payload.FilePath() != "generated/x.go" {
		t.Fatalf("file path = %q", payload.FilePath())
	}
}

func TestNormalizeCursorPayloadBeforeShellExecution(t *testing.T) {
	body, err := NormalizeCursorPayload("cursor-before-shell-execution", []byte(`{
		"sessionId":"cursor-2",
		"command":"git reset --hard"
	}`))
	if err != nil {
		t.Fatalf("NormalizeCursorPayload: %v", err)
	}
	payload, err := ParsePayload(body)
	if err != nil {
		t.Fatalf("ParsePayload: %v", err)
	}
	if !payload.IsCommandTool() {
		t.Fatalf("expected command tool, got %q", payload.ToolName)
	}
	if payload.Command() != "git reset --hard" {
		t.Fatalf("command = %q", payload.Command())
	}
}

func TestCursorBeforeSubmitPromptActivatesRunLoopFlag(t *testing.T) {
	repo := setupPolicyRepo(t)
	body, err := NormalizeCursorPayload("cursor-user-prompt-submit", []byte(`{
		"sessionId":"cursor-runLoop",
		"text":"arbeite autonom /runloop und nutze den restlichen Prompt"
	}`))
	if err != nil {
		t.Fatalf("NormalizeCursorPayload: %v", err)
	}
	result := RunUserPromptSubmit(repo, body)
	if result.ExitCode != 0 {
		t.Fatalf("RunUserPromptSubmit: exit=%d stderr=%s", result.ExitCode, result.Stderr)
	}
	state, err := loadRunLoopState(repo)
	if err != nil {
		t.Fatalf("loadRunLoopState: %v", err)
	}
	if !state.Enabled || state.SessionID != "cursor-runLoop" {
		t.Fatalf("expected Cursor /runloop flag prompt to enable state, got %#v", state)
	}
}

func TestPayloadLooksLikeCursor(t *testing.T) {
	tests := []struct {
		name string
		body string
		want bool
	}{
		{name: "cursor version", body: `{"session_id":"s","cursor_version":"3.5.17"}`, want: true},
		{name: "cursor workspace hook", body: `{"session_id":"s","hook_event_name":"stop","workspace_roots":["/repo"]}`, want: true},
		{name: "cursor conversation generation", body: `{"conversation_id":"c","generation_id":"g"}`, want: true},
		{name: "plain claude payload", body: `{"session_id":"s","tool_name":"Read"}`, want: false},
		{name: "cursor marker inside value", body: `{"session_id":"s","message":"\"cursor_version\" is documentation text"}`, want: false},
		{name: "invalid json", body: `{`, want: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := PayloadLooksLikeCursor([]byte(tc.body)); got != tc.want {
				t.Fatalf("PayloadLooksLikeCursor() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestNormalizeCursorPayloadAfterShellExecutionFailure(t *testing.T) {
	body, err := NormalizeCursorPayload("cursor-after-shell-execution", []byte(`{
		"conversationId":"cursor-3",
		"command":"go test ./...",
		"exitCode":1,
		"stderr":"failed"
	}`))
	if err != nil {
		t.Fatalf("NormalizeCursorPayload: %v", err)
	}
	payload, err := ParsePayload(body)
	if err != nil {
		t.Fatalf("ParsePayload: %v", err)
	}
	exitCode := payload.ExitCode()
	if exitCode == nil || *exitCode != 1 {
		t.Fatalf("exit code = %#v", exitCode)
	}
	if payload.Error != "" {
		t.Fatalf("top-level error should remain empty unless Cursor sends it, got %q", payload.Error)
	}
}

func TestAdaptCursorPreDecisionDeny(t *testing.T) {
	result := AdaptCursorResult("cursor-pre-tool-use", Result{ExitCode: 2, Stderr: "blocked"})
	if result.ExitCode != 0 {
		t.Fatalf("Cursor deny response must exit 0, got %d", result.ExitCode)
	}
	var payload map[string]interface{}
	if err := json.Unmarshal([]byte(result.Stdout), &payload); err != nil {
		t.Fatalf("stdout is not JSON: %v\n%s", err, result.Stdout)
	}
	if payload["permission"] != "deny" || payload["user_message"] != "blocked" {
		t.Fatalf("unexpected deny payload: %#v", payload)
	}
}

func TestAdaptCursorSuccessOutputsAllowJSON(t *testing.T) {
	for _, event := range []string{
		"cursor-session-start",
		"cursor-user-prompt-submit",
		"cursor-pre-tool-use",
		"cursor-before-shell-execution",
		"cursor-before-read-file",
		"cursor-post-tool-use",
		"cursor-after-file-edit",
		"cursor-stop",
	} {
		t.Run(event, func(t *testing.T) {
			result := AdaptCursorResult(event, Result{ExitCode: 0})
			if result.ExitCode != 0 {
				t.Fatalf("exit = %d", result.ExitCode)
			}
			var payload map[string]interface{}
			if err := json.Unmarshal([]byte(result.Stdout), &payload); err != nil {
				t.Fatalf("stdout is not JSON: %v\n%s", err, result.Stdout)
			}
			if payload["continue"] != true || payload["permission"] != "allow" {
				t.Fatalf("expected explicit Cursor allow JSON, got %#v", payload)
			}
		})
	}
}

func TestAdaptCursorStopFollowup(t *testing.T) {
	result := AdaptCursorResult("cursor-stop", Result{ExitCode: 0, Stdout: `{"decision":"block","reason":"fix it"}`})
	if result.ExitCode != 0 {
		t.Fatalf("Cursor stop followup must exit 0, got %d", result.ExitCode)
	}
	if !strings.Contains(result.Stdout, `"followup_message":"fix it"`) {
		t.Fatalf("expected followup message, got %s", result.Stdout)
	}
}
