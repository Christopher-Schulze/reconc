package agentsession

import (
	"encoding/json"
	"strings"
	"testing"

	"reconc.dev/reconc/internal/policy"
)

func TestNormalizeZCodePayloadCoversEveryNativeEvent(t *testing.T) {
	repo := t.TempDir()
	tests := []struct {
		route  string
		event  string
		extra  map[string]interface{}
		mcp    bool
		result string
	}{
		{route: "zcode-session-start", event: "SessionStart"},
		{route: "zcode-user-prompt-submit", event: "UserPromptSubmit", extra: map[string]interface{}{"prompt": "inspect"}},
		{route: "zcode-pre-tool-use", event: "PreToolUse", extra: zcodeToolFixture(), mcp: true},
		{route: "zcode-permission-request", event: "PermissionRequest", extra: map[string]interface{}{"tool_name": "Bash", "tool_input": map[string]interface{}{"command": "go test ./..."}}},
		{route: "zcode-post-tool-use", event: "PostToolUse", extra: mergeZCodeFixture(zcodeToolFixture(), map[string]interface{}{"tool_response": map[string]interface{}{"exit_code": 0}}), mcp: true, result: "success"},
		{route: "zcode-post-tool-use-failure", event: "PostToolUseFailure", extra: mergeZCodeFixture(zcodeToolFixture(), map[string]interface{}{"error": "failed", "is_interrupt": false}), mcp: true, result: "failure"},
		{route: "zcode-stop", event: "Stop", extra: map[string]interface{}{"stop_hook_active": false}},
	}
	for _, test := range tests {
		t.Run(test.event, func(t *testing.T) {
			payload := map[string]interface{}{
				"hook_event_name": test.event,
				"session_id":      "ses_fixture",
				"cwd":             repo,
			}
			mergeZCodeFixture(payload, test.extra)
			body, err := json.Marshal(payload)
			if err != nil {
				t.Fatal(err)
			}
			normalizedBody, err := NormalizeZCodePayload(test.route, body, repo)
			if err != nil {
				t.Fatal(err)
			}
			var normalized zcodeNormalizedPayload
			if err := json.Unmarshal(normalizedBody, &normalized); err != nil {
				t.Fatal(err)
			}
			if normalized.ReconcRuntime != "zcode" || normalized.ZCodeEvent != test.route || normalized.SessionID != "ses_fixture" {
				t.Fatalf("normalized payload = %+v", normalized)
			}
			if test.mcp {
				if normalized.MCP == nil || normalized.MCP.Platform != policy.MCPPlatformZCode || normalized.MCP.Tool != "Bash" || !normalized.MCP.BlockingPreHook || !normalized.MCP.InputValid || normalized.MCP.Outcome != test.result {
					t.Fatalf("normalized MCP envelope = %+v", normalized.MCP)
				}
			} else if normalized.MCP != nil {
				t.Fatalf("non-tool event acquired MCP envelope: %+v", normalized.MCP)
			}
		})
	}
}

func TestNormalizeRuntimeNameRecognizesZCodeRoutes(t *testing.T) {
	for _, event := range []string{"zcode-session-start", "zcode-pre-tool-use", "zcode-stop"} {
		if got := normalizeRuntimeName(event); got != "zcode" {
			t.Fatalf("normalizeRuntimeName(%q) = %q, want zcode", event, got)
		}
	}
}

func TestNormalizeZCodePayloadRejectsInvalidContracts(t *testing.T) {
	repo := t.TempDir()
	validTool := zcodeToolFixture()
	tests := []struct {
		name    string
		route   string
		payload interface{}
		want    string
	}{
		{name: "unknown route", route: "zcode-unknown", payload: map[string]interface{}{}, want: "unsupported ZCode hook route"},
		{name: "empty", route: "zcode-session-start", payload: nil, want: "empty ZCode payload"},
		{name: "wrong event", route: "zcode-session-start", payload: map[string]interface{}{"hook_event_name": "Stop", "session_id": "ses", "cwd": repo}, want: "does not match route"},
		{name: "missing session", route: "zcode-session-start", payload: map[string]interface{}{"hook_event_name": "SessionStart", "cwd": repo}, want: "session_id"},
		{name: "outside cwd", route: "zcode-session-start", payload: map[string]interface{}{"hook_event_name": "SessionStart", "session_id": "ses", "cwd": t.TempDir()}, want: "outside"},
		{name: "missing tool name", route: "zcode-pre-tool-use", payload: mergeZCodeFixture(map[string]interface{}{"hook_event_name": "PreToolUse", "session_id": "ses", "cwd": repo}, map[string]interface{}{"tool_use_id": "id", "tool_input": map[string]interface{}{}}), want: "tool_name"},
		{name: "missing tool id", route: "zcode-pre-tool-use", payload: mergeZCodeFixture(map[string]interface{}{"hook_event_name": "PreToolUse", "session_id": "ses", "cwd": repo}, map[string]interface{}{"tool_name": "Bash", "tool_input": map[string]interface{}{}}), want: "tool_use_id"},
		{name: "invalid tool input", route: "zcode-pre-tool-use", payload: mergeZCodeFixture(map[string]interface{}{"hook_event_name": "PreToolUse", "session_id": "ses", "cwd": repo}, map[string]interface{}{"tool_name": "Bash", "tool_use_id": "id", "tool_input": []interface{}{}}), want: "tool_input"},
		{name: "missing response", route: "zcode-post-tool-use", payload: mergeZCodeFixture(map[string]interface{}{"hook_event_name": "PostToolUse", "session_id": "ses", "cwd": repo}, validTool), want: "tool_response"},
		{name: "missing failure error", route: "zcode-post-tool-use-failure", payload: mergeZCodeFixture(map[string]interface{}{"hook_event_name": "PostToolUseFailure", "session_id": "ses", "cwd": repo}, mergeZCodeFixture(zcodeToolFixture(), map[string]interface{}{"is_interrupt": false})), want: "missing error"},
		{name: "missing interrupt", route: "zcode-post-tool-use-failure", payload: mergeZCodeFixture(map[string]interface{}{"hook_event_name": "PostToolUseFailure", "session_id": "ses", "cwd": repo}, mergeZCodeFixture(zcodeToolFixture(), map[string]interface{}{"error": "failed"})), want: "is_interrupt"},
		{name: "missing stop state", route: "zcode-stop", payload: map[string]interface{}{"hook_event_name": "Stop", "session_id": "ses", "cwd": repo}, want: "stop_hook_active"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var body []byte
			if test.payload != nil {
				var err error
				body, err = json.Marshal(test.payload)
				if err != nil {
					t.Fatal(err)
				}
			}
			if _, err := NormalizeZCodePayload(test.route, body, repo); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want %q", err, test.want)
			}
		})
	}
	valid := map[string]interface{}{"hook_event_name": "SessionStart", "session_id": "ses", "cwd": repo}
	body, err := json.Marshal(valid)
	if err != nil {
		t.Fatal(err)
	}
	body = append(body, body...)
	if _, err := NormalizeZCodePayload("zcode-session-start", body, repo); err == nil || !strings.Contains(err.Error(), "multiple JSON values") {
		t.Fatalf("multiple-value error = %v", err)
	}
}

func zcodeToolFixture() map[string]interface{} {
	return map[string]interface{}{
		"tool_name":   "Bash",
		"tool_input":  map[string]interface{}{"command": "go test ./..."},
		"tool_use_id": "tool_fixture",
	}
}

func mergeZCodeFixture(destination map[string]interface{}, source map[string]interface{}) map[string]interface{} {
	for key, value := range source {
		destination[key] = value
	}
	return destination
}
