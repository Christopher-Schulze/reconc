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

func TestNormalizeCursorBeforeSubmitPrompt(t *testing.T) {
	body, err := NormalizeCursorPayload("cursor-user-prompt-submit", []byte(`{
		"sessionId":"cursor-prompt",
		"text":"run the repository checks"
	}`))
	if err != nil {
		t.Fatalf("NormalizeCursorPayload: %v", err)
	}
	payload, err := ParsePayload(body)
	if err != nil {
		t.Fatalf("ParsePayload: %v", err)
	}
	if payload.SessionID != "cursor-prompt" || payload.Prompt != "run the repository checks" {
		t.Fatalf("normalized prompt payload = %#v", payload)
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

func TestNormalizeCursorPayloadAfterShellExecutionIsPassive(t *testing.T) {
	body, err := NormalizeCursorPayload("cursor-after-shell-execution", []byte(`{
		"conversationId":"cursor-3",
		"command":"go test ./...",
		"output":"failed",
		"duration":1200
	}`))
	if err != nil {
		t.Fatalf("NormalizeCursorPayload: %v", err)
	}
	payload, err := ParsePayload(body)
	if err != nil {
		t.Fatalf("ParsePayload: %v", err)
	}
	if payload.ToolName != "" || payload.Command() != "" || payload.ExitCode() != nil {
		t.Fatalf("afterShellExecution must not normalize into tool evidence: %#v", payload)
	}
}

func TestNormalizeCursorPayloadPostToolUseFailure(t *testing.T) {
	body, err := NormalizeCursorPayload("cursor-post-tool-use-failure", []byte(`{
		"conversation_id":"cursor-failure",
		"tool_name":"Shell",
		"tool_input":{"command":"go test ./..."},
		"tool_use_id":"tool-17",
		"error_message":"process exited unsuccessfully",
		"failure_type":"error",
		"is_interrupt":false
	}`))
	if err != nil {
		t.Fatalf("NormalizeCursorPayload: %v", err)
	}
	payload, err := ParsePayload(body)
	if err != nil {
		t.Fatalf("ParsePayload: %v", err)
	}
	if !payload.IsCommandTool() || payload.Command() != "go test ./..." {
		t.Fatalf("failure command normalization = %#v", payload)
	}
	if payload.ToolUseID != "tool-17" || payload.Error != "process exited unsuccessfully" {
		t.Fatalf("failure metadata normalization = %#v", payload)
	}
	if len(payload.ToolResponse) != 0 {
		t.Fatalf("failure route must not fabricate a tool response: %#v", payload.ToolResponse)
	}
}

func TestNormalizeCursorPayloadRequiresSessionIdentity(t *testing.T) {
	if _, err := NormalizeCursorPayload("cursor-post-tool-use", []byte(`{
		"tool_name":"Write",
		"tool_input":{"file_path":"src/app.go"}
	}`)); err == nil || !strings.Contains(err.Error(), "session identity") {
		t.Fatalf("missing identity error = %v", err)
	}
}

func TestNormalizeCursorWorkspaceOpenIsSessionlessAndPrivate(t *testing.T) {
	body, err := NormalizeCursorPayload("cursor-workspace-open", []byte(`{
		"hook_event_name":"workspaceOpen",
		"cursor_version":"3.13.21",
		"workspace_roots":["/repo"],
		"user_email":"user@example.invalid"
	}`))
	if err != nil {
		t.Fatalf("NormalizeCursorPayload: %v", err)
	}
	var payload map[string]interface{}
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("decode normalized payload: %v", err)
	}
	if payload["cursor_event"] != "cursor-workspace-open" || payload["cursor_version"] != "3.13.21" {
		t.Fatalf("normalized workspace payload = %#v", payload)
	}
	if _, present := payload["session_id"]; present {
		t.Fatalf("workspace lifecycle fabricated a session identity: %#v", payload)
	}
	if _, present := payload["user_email"]; present {
		t.Fatalf("workspace lifecycle retained user identity: %#v", payload)
	}
}

func TestNormalizeCursorWorkspaceOpenRejectsMalformedContract(t *testing.T) {
	tests := []string{
		`{"hook_event_name":"stop","cursor_version":"3.13.21","workspace_roots":["/repo"]}`,
		`{"hook_event_name":"workspaceOpen","workspace_roots":["/repo"]}`,
		`{"hook_event_name":"workspaceOpen","cursor_version":"3.13.21","workspace_roots":[]}`,
		`{"hook_event_name":"workspaceOpen","cursor_version":"3.13.21","workspace_roots":[1]}`,
	}
	for _, payload := range tests {
		if _, err := NormalizeCursorPayload("cursor-workspace-open", []byte(payload)); err == nil {
			t.Fatalf("malformed workspace payload passed: %s", payload)
		}
	}
}

func TestNormalizeCursorSubagentUsesChildIdentity(t *testing.T) {
	normalized, err := NormalizeCursorPayload("cursor-subagent-stop", []byte(`{
		"conversation_id":"parent-session",
		"subagent_id":"child-session",
		"hook_event_name":"subagentStop"
	}`))
	if err != nil {
		t.Fatal(err)
	}
	payload, err := ParsePayload(normalized)
	if err != nil {
		t.Fatal(err)
	}
	if payload.SessionID != "child-session" {
		t.Fatalf("subagent session identity = %q", payload.SessionID)
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

func TestAdaptCursorBeforeSubmitPromptUsesNativeDecision(t *testing.T) {
	blocked := AdaptCursorResult("cursor-user-prompt-submit", Result{ExitCode: 2, Stderr: "blocked"})
	if blocked.ExitCode != 0 || !strings.Contains(blocked.Stdout, `"continue":false`) || !strings.Contains(blocked.Stdout, `"user_message":"blocked"`) {
		t.Fatalf("blocked prompt result = %#v", blocked)
	}
	allowed := AdaptCursorResult("cursor-user-prompt-submit", Result{ExitCode: 0})
	if allowed.ExitCode != 0 || allowed.Stdout != `{"continue":true}` {
		t.Fatalf("allowed prompt result = %#v", allowed)
	}
}

func TestAdaptCursorPreDecisionSuccessOutputsAllowJSON(t *testing.T) {
	for _, event := range []string{
		"cursor-pre-tool-use",
		"cursor-before-shell-execution",
		"cursor-before-read-file",
		"cursor-subagent-start",
	} {
		t.Run(event, func(t *testing.T) {
			result := AdaptCursorResult(event, Result{ExitCode: 0, Stdout: `{"internal":"ignored"}`})
			if result.ExitCode != 0 {
				t.Fatalf("exit = %d", result.ExitCode)
			}
			var payload map[string]interface{}
			if err := json.Unmarshal([]byte(result.Stdout), &payload); err != nil {
				t.Fatalf("stdout is not JSON: %v\n%s", err, result.Stdout)
			}
			if payload["permission"] != "allow" {
				t.Fatalf("expected explicit Cursor allow JSON, got %#v", payload)
			}
		})
	}
}

func TestAdaptCursorOutputlessObservationsUseEmptyObject(t *testing.T) {
	for _, event := range []string{
		"cursor-session-start",
		"cursor-post-tool-use",
		"cursor-post-tool-use-failure",
		"cursor-after-shell-execution",
		"cursor-after-file-edit",
		"cursor-pre-compaction",
		"cursor-workspace-open",
	} {
		t.Run(event, func(t *testing.T) {
			result := AdaptCursorResult(event, Result{ExitCode: 0, Stdout: `{"ignored":true}`, Stderr: "warning"})
			if result.ExitCode != 0 || result.Stdout != "{}" || result.Stderr != "warning" {
				t.Fatalf("outputless result = %#v", result)
			}
		})
	}
	for _, event := range []string{"cursor-stop", "cursor-subagent-stop"} {
		result := AdaptCursorResult(event, Result{ExitCode: 0})
		if result.ExitCode != 0 || result.Stdout != "{}" {
			t.Fatalf("%s empty stop result = %#v", event, result)
		}
	}
}

func TestCursorEventClassifiersAreDisjoint(t *testing.T) {
	for _, event := range []string{
		"cursor-session-start", "cursor-session-end", "cursor-pre-compaction", "cursor-workspace-open",
		"cursor-pre-tool-use", "cursor-before-shell-execution", "cursor-before-mcp-execution",
		"cursor-before-read-file", "cursor-before-tab-file-read", "cursor-subagent-start",
		"cursor-post-tool-use", "cursor-after-mcp-execution", "cursor-after-file-edit", "cursor-after-tab-file-edit",
		"cursor-post-tool-use-failure", "cursor-after-shell-execution",
	} {
		classifications := 0
		for _, matched := range []bool{
			isCursorFireAndForgetEvent(event), isCursorPreDecisionEvent(event), isCursorObservationEvent(event),
		} {
			if matched {
				classifications++
			}
		}
		if classifications > 1 {
			t.Fatalf("%s belongs to %d event classifiers", event, classifications)
		}
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
