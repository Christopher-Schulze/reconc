package agentsession

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func TestNormalizePiPayloadCoversEveryNativeRoute(t *testing.T) {
	repo := t.TempDir()
	for route, nativeEvent := range piNativeEvents {
		route := route
		nativeEvent := nativeEvent
		t.Run(route, func(t *testing.T) {
			payload := map[string]interface{}{
				"hook_event_name": nativeEvent,
				"session_id":      "pi-s1",
				"cwd":             repo,
			}
			switch route {
			case "pi-session-start":
				payload["reason"] = "startup"
			case "pi-user-prompt-submit":
				payload["prompt"] = "ship it"
				payload["input_source"] = "interactive"
			case "pi-pre-tool-use":
				addPiToolPayload(payload, false)
			case "pi-user-bash":
				payload["tool_name"] = "bash"
				payload["tool_input"] = map[string]interface{}{"command": "go test ./..."}
				payload["user_bash_cwd"] = repo
				payload["exclude_from_context"] = false
			case "pi-post-tool-use":
				addPiToolPayload(payload, false)
				payload["is_error"] = false
				payload["tool_response"] = map[string]interface{}{"success": true}
			case "pi-post-tool-use-failure":
				addPiToolPayload(payload, true)
				payload["is_error"] = true
				payload["tool_response"] = map[string]interface{}{"success": false, "error": "failed"}
			case "pi-stop":
				payload["stop_hook_active"] = false
			case "pi-continuation-requested", "pi-continuation-failed", "pi-continuation-suppressed":
				payload["continuation_delivery"] = strings.TrimPrefix(route, "pi-continuation-")
			case "pi-session-end":
				payload["reason"] = "quit"
			case "pi-pre-compaction", "pi-post-compaction":
				payload["reason"] = "threshold"
				payload["will_retry"] = false
			}
			body, err := json.Marshal(payload)
			if err != nil {
				t.Fatal(err)
			}
			normalized, err := NormalizePiPayload(route, body, repo)
			if err != nil {
				t.Fatalf("NormalizePiPayload: %v", err)
			}
			parsed, err := ParsePayload(normalized)
			if err != nil {
				t.Fatal(err)
			}
			if parsed.SessionID != "pi-s1" || parsed.Raw["reconc_runtime"] != "pi" || parsed.Raw["pi_event"] != route {
				t.Fatalf("normalized Pi identity = %#v", parsed.Raw)
			}
			if route == "pi-stop" && (parsed.StrictContinuation || parsed.StopHookActive) {
				t.Fatalf("Pi settled Stop must remain asynchronous: %#v", parsed.Raw)
			}
		})
	}
}

func TestNormalizePiPayloadPreservesToolAndMCPIdentity(t *testing.T) {
	repo := t.TempDir()
	payload := fmt.Sprintf(`{
		"hook_event_name":"tool_call",
		"session_id":"pi-s1",
		"cwd":%q,
		"tool_name":"custom.mcp.tool",
		"tool_call_id":"call-1",
		"tool_input":{"path":"README.md"}
	}`, repo)
	body, err := NormalizePiPayload("pi-pre-tool-use", []byte(payload), repo)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := ParsePayload(body)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.ToolName != "custom.mcp.tool" || parsed.ToolUseID != "call-1" || parsed.MCP == nil {
		t.Fatalf("Pi tool identity lost: %#v", parsed)
	}
	mcpEnvelope, _ := parsed.Raw["reconc_mcp"].(map[string]interface{})
	if parsed.MCP.Platform != "pi" || parsed.MCP.Tool != "custom.mcp.tool" || mcpEnvelope["observed"] != false || !parsed.MCP.BlockingPreHook || !parsed.MCP.InputValid {
		t.Fatalf("Pi MCP envelope = %#v", parsed.MCP)
	}
}

func TestNormalizePiPayloadRejectsUnsafeShapes(t *testing.T) {
	repo := t.TempDir()
	outside := t.TempDir()
	tests := []struct {
		name  string
		route string
		body  string
		want  string
	}{
		{name: "unknown route", route: "pi-unknown", body: `{}`, want: "unsupported Pi hook route"},
		{name: "empty", route: "pi-stop", body: ``, want: "empty Pi payload"},
		{name: "route mismatch", route: "pi-stop", body: fmt.Sprintf(`{"hook_event_name":"tool_call","session_id":"s1","cwd":%q}`, repo), want: "does not match route"},
		{name: "missing session", route: "pi-stop", body: fmt.Sprintf(`{"hook_event_name":"agent_settled","cwd":%q,"stop_hook_active":false}`, repo), want: "missing non-empty session_id"},
		{name: "cwd escape", route: "pi-stop", body: fmt.Sprintf(`{"hook_event_name":"agent_settled","session_id":"s1","cwd":%q,"stop_hook_active":false}`, outside), want: "outside repository root"},
		{name: "missing tool call", route: "pi-pre-tool-use", body: fmt.Sprintf(`{"hook_event_name":"tool_call","session_id":"s1","cwd":%q,"tool_name":"write","tool_input":{}}`, repo), want: "missing tool_call_id"},
		{name: "non-object input", route: "pi-pre-tool-use", body: fmt.Sprintf(`{"hook_event_name":"tool_call","session_id":"s1","cwd":%q,"tool_name":"write","tool_call_id":"c","tool_input":[]}`, repo), want: "must be a JSON object"},
		{name: "user bash cwd escape", route: "pi-user-bash", body: fmt.Sprintf(`{"hook_event_name":"user_bash","session_id":"s1","cwd":%q,"tool_name":"bash","tool_input":{"command":"true"},"user_bash_cwd":%q,"exclude_from_context":false}`, repo, outside), want: "outside repository root"},
		{name: "result mismatch", route: "pi-post-tool-use", body: fmt.Sprintf(`{"hook_event_name":"tool_result","session_id":"s1","cwd":%q,"tool_name":"bash","tool_call_id":"c","tool_input":{},"tool_response":{},"is_error":true}`, repo), want: "does not match route"},
		{name: "stop active", route: "pi-stop", body: fmt.Sprintf(`{"hook_event_name":"agent_settled","session_id":"s1","cwd":%q,"stop_hook_active":true}`, repo), want: "stop_hook_active=false"},
		{name: "delivery mismatch", route: "pi-continuation-requested", body: fmt.Sprintf(`{"hook_event_name":"agent_settled","session_id":"s1","cwd":%q,"continuation_delivery":"failed"}`, repo), want: "does not match route"},
		{name: "bad start reason", route: "pi-session-start", body: fmt.Sprintf(`{"hook_event_name":"session_start","session_id":"s1","cwd":%q,"reason":"other"}`, repo), want: "invalid Pi session_start reason"},
		{name: "trailing value", route: "pi-stop", body: fmt.Sprintf(`{"hook_event_name":"agent_settled","session_id":"s1","cwd":%q,"stop_hook_active":false} {}`, repo), want: "multiple JSON values"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := NormalizePiPayload(test.route, []byte(test.body), repo)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want containing %q", err, test.want)
			}
		})
	}
}

func addPiToolPayload(payload map[string]interface{}, failure bool) {
	payload["tool_name"] = "bash"
	payload["tool_call_id"] = "call-1"
	payload["tool_input"] = map[string]interface{}{"command": "go test ./..."}
	if failure {
		payload["error"] = "failed"
	}
}
