package agentsession

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func TestNormalizeGitHubCopilotPayloadUsesDocumentedCompatibilityShape(t *testing.T) {
	repo := t.TempDir()
	payload := []byte(fmt.Sprintf(`{
		"hook_event_name":"PreToolUse",
		"session_id":"copilot-session",
		"cwd":%q,
		"tool_name":"Edit",
		"tool_input":{"file_path":"generated/blocked.go"}
	}`, repo))
	body, err := NormalizeGitHubCopilotPayload("copilot-pre-tool-use", payload, repo)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := ParsePayload(body)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.SessionID != "copilot-session" || parsed.ToolName != "Edit" || parsed.FilePath() != "generated/blocked.go" {
		t.Fatalf("normalized payload = %+v", parsed)
	}
	if parsed.Raw["reconc_runtime"] != "github-copilot" || parsed.Raw["copilot_event"] != "copilot-pre-tool-use" {
		t.Fatalf("runtime identity missing: %#v", parsed.Raw)
	}
}

func TestNormalizeGitHubCopilotPayloadMapsToolResult(t *testing.T) {
	repo := t.TempDir()
	payload := []byte(fmt.Sprintf(`{
		"hook_event_name":"PostToolUse",
		"session_id":"copilot-post",
		"cwd":%q,
		"tool_name":"Bash",
		"tool_input":{"command":"go test ./..."},
		"tool_result":{"result_type":"success","text_result_for_llm":"ok"}
	}`, repo))
	body, err := NormalizeGitHubCopilotPayload("copilot-post-tool-use", payload, repo)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := ParsePayload(body)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Command() != "go test ./..." || parsed.ToolResponse["result_type"] != "success" {
		t.Fatalf("normalized post payload = %+v", parsed)
	}
}

func TestNormalizeGitHubCopilotPayloadAcceptsCamelSubagentStart(t *testing.T) {
	repo := t.TempDir()
	payload := []byte(fmt.Sprintf(`{"sessionId":"copilot-sub","cwd":%q,"agentName":"research"}`, repo))
	body, err := NormalizeGitHubCopilotPayload("copilot-subagent-start", payload, repo)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := ParsePayload(body)
	if err != nil || parsed.SessionID != "copilot-sub" {
		t.Fatalf("camel subagent payload = %+v, %v", parsed, err)
	}
}

func TestNormalizeGitHubCopilotPayloadRejectsUnsafeShapes(t *testing.T) {
	repo := t.TempDir()
	other := t.TempDir()
	tests := []struct {
		name    string
		event   string
		payload string
		want    string
	}{
		{name: "route mismatch", event: "copilot-pre-tool-use", payload: fmt.Sprintf(`{"hook_event_name":"PostToolUse","session_id":"s","cwd":%q,"tool_name":"Edit","tool_input":{}}`, repo), want: "does not match route"},
		{name: "workspace mismatch", event: "copilot-stop", payload: fmt.Sprintf(`{"hook_event_name":"Stop","session_id":"s","cwd":%q}`, other), want: "outside repository root"},
		{name: "missing session", event: "copilot-stop", payload: fmt.Sprintf(`{"hook_event_name":"Stop","cwd":%q}`, repo), want: "session_id"},
		{name: "missing tool input", event: "copilot-pre-tool-use", payload: fmt.Sprintf(`{"hook_event_name":"PreToolUse","session_id":"s","cwd":%q,"tool_name":"Edit"}`, repo), want: "tool_input"},
		{name: "unsafe scalar edit input", event: "copilot-pre-tool-use", payload: fmt.Sprintf(`{"hook_event_name":"PreToolUse","session_id":"s","cwd":%q,"tool_name":"Edit","tool_input":"unsafe"}`, repo), want: "cannot be evaluated safely"},
		{name: "non-boolean stop state", event: "copilot-stop", payload: fmt.Sprintf(`{"hook_event_name":"Stop","session_id":"s","cwd":%q,"stop_hook_active":"false"}`, repo), want: "JSON boolean"},
		{name: "multiple values", event: "copilot-stop", payload: fmt.Sprintf(`{"hook_event_name":"Stop","session_id":"s","cwd":%q} {}`, repo), want: "multiple JSON values"},
		{name: "deep payload", event: "copilot-stop", payload: strings.Repeat("[", MaxJSONDepth+1) + strings.Repeat("]", MaxJSONDepth+1), want: "nesting"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := NormalizeGitHubCopilotPayload(test.event, []byte(test.payload), repo)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestNormalizeGitHubCopilotPayloadAcceptsDocumentedUnknownBashInput(t *testing.T) {
	repo := t.TempDir()
	payload := []byte(fmt.Sprintf(`{"hook_event_name":"PreToolUse","session_id":"s","cwd":%q,"tool_name":"Bash","tool_input":"go test ./..."}`, repo))
	body, err := NormalizeGitHubCopilotPayload("copilot-pre-tool-use", payload, repo)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := ParsePayload(body)
	if err != nil || parsed.Command() != "go test ./..." {
		t.Fatalf("normalized scalar Bash input = %+v, %v", parsed, err)
	}
}

func TestNormalizeGitHubCopilotPayloadParsesStringifiedToolInput(t *testing.T) {
	repo := t.TempDir()
	payload := []byte(fmt.Sprintf(`{"hook_event_name":"PreToolUse","session_id":"s","cwd":%q,"tool_name":"Edit","tool_input":"{\"file_path\":\"generated/blocked.go\"}"}`, repo))
	body, err := NormalizeGitHubCopilotPayload("copilot-pre-tool-use", payload, repo)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := ParsePayload(body)
	if err != nil || parsed.FilePath() != "generated/blocked.go" {
		t.Fatalf("normalized stringified tool input = %+v, %v", parsed, err)
	}
}

func TestNormalizeGitHubCopilotPayloadCoversEveryGeneratedRoute(t *testing.T) {
	repo := t.TempDir()
	tests := []struct {
		event   string
		payload string
	}{
		{event: "copilot-session-start", payload: `{"hook_event_name":"SessionStart","session_id":"session-start","cwd":%q,"source":"startup"}`},
		{event: "copilot-user-prompt-submit", payload: `{"hook_event_name":"UserPromptSubmit","session_id":"prompt","cwd":%q,"prompt":"continue"}`},
		{event: "copilot-pre-tool-use", payload: `{"hook_event_name":"PreToolUse","session_id":"pre","cwd":%q,"tool_name":"Edit","tool_input":{"file_path":"src/a.go"}}`},
		{event: "copilot-permission-request", payload: `{"hook_event_name":"PermissionRequest","session_id":"permission","cwd":%q,"tool_name":"Bash","tool_input":{"command":"go test ./..."}}`},
		{event: "copilot-post-tool-use", payload: `{"hook_event_name":"PostToolUse","session_id":"post","cwd":%q,"tool_name":"Read","tool_input":{"file_path":"README.md"},"tool_result":{"result_type":"success","text_result_for_llm":"ok"}}`},
		{event: "copilot-post-tool-use-failure", payload: `{"hook_event_name":"PostToolUseFailure","session_id":"failure","cwd":%q,"tool_name":"Bash","tool_input":{"command":"go test ./..."},"error":"exit 1"}`},
		{event: "copilot-stop", payload: `{"hook_event_name":"Stop","session_id":"stop","cwd":%q,"stop_hook_active":false}`},
		{event: "copilot-session-end", payload: `{"hook_event_name":"SessionEnd","session_id":"end","cwd":%q,"reason":"complete"}`},
		{event: "copilot-notification", payload: `{"hook_event_name":"Notification","sessionId":"notification","cwd":%q,"notification_type":"agent_idle"}`},
		{event: "copilot-subagent-start", payload: `{"sessionId":"sub-start","cwd":%q,"agentName":"research"}`},
		{event: "copilot-subagent-stop", payload: `{"hook_event_name":"SubagentStop","session_id":"sub-stop","cwd":%q,"agent_name":"research","stop_reason":"end_turn"}`},
		{event: "copilot-pre-compaction", payload: `{"hook_event_name":"PreCompact","session_id":"compact","cwd":%q,"trigger":"auto"}`},
	}
	for _, test := range tests {
		t.Run(test.event, func(t *testing.T) {
			body, err := NormalizeGitHubCopilotPayload(test.event, []byte(fmt.Sprintf(test.payload, repo)), repo)
			if err != nil {
				t.Fatal(err)
			}
			parsed, err := ParsePayload(body)
			if err != nil || parsed.SessionID == "" || parsed.Raw["copilot_event"] != test.event {
				t.Fatalf("normalized route = %+v, %v", parsed, err)
			}
		})
	}
}

func TestAdaptGitHubCopilotResultUsesExactDecisionContracts(t *testing.T) {
	pre := AdaptGitHubCopilotResult("copilot-pre-tool-use", Result{ExitCode: 2, Stderr: "blocked"})
	var preDecision map[string]interface{}
	if pre.ExitCode != 0 || json.Unmarshal([]byte(pre.Stdout), &preDecision) != nil ||
		preDecision["permissionDecision"] != "deny" || preDecision["permissionDecisionReason"] != "blocked" {
		t.Fatalf("pre-tool decision = %+v %#v", pre, preDecision)
	}
	if allowed := AdaptGitHubCopilotResult("copilot-pre-tool-use", Result{}); allowed.Stdout != "" || allowed.ExitCode != 0 {
		t.Fatalf("allowed pre-tool result must fall through: %+v", allowed)
	}

	body, err := permissionRequestDenyJSONOutput("policy")
	if err != nil {
		t.Fatal(err)
	}
	permission := AdaptGitHubCopilotResult("copilot-permission-request", Result{Stdout: body})
	var permissionDecision map[string]interface{}
	if json.Unmarshal([]byte(permission.Stdout), &permissionDecision) != nil ||
		permissionDecision["behavior"] != "deny" || permissionDecision["message"] != "policy" {
		t.Fatalf("permission decision = %+v %#v", permission, permissionDecision)
	}

	postFailure := AdaptGitHubCopilotResult("copilot-post-tool-use-failure", Result{Stdout: `{"hookSpecificOutput":{"additionalContext":"retry after fixing tests"}}`})
	var context map[string]interface{}
	if json.Unmarshal([]byte(postFailure.Stdout), &context) != nil || context["additionalContext"] != "retry after fixing tests" {
		t.Fatalf("post failure output = %+v %#v", postFailure, context)
	}

	stop := AdaptGitHubCopilotResult("copilot-stop", Result{ExitCode: 2, Stderr: "runtime failed"})
	var stopDecision map[string]interface{}
	if stop.ExitCode != 0 || json.Unmarshal([]byte(stop.Stdout), &stopDecision) != nil ||
		stopDecision["decision"] != "block" || !strings.Contains(stopDecision["reason"].(string), "runtime failed") {
		t.Fatalf("stop decision = %+v %#v", stop, stopDecision)
	}
}

func TestNormalizeRuntimeNameRecognizesGitHubCopilot(t *testing.T) {
	for _, input := range []string{"github-copilot", "github-copilot-stop", "copilot", "copilot-stop"} {
		if got := normalizeRuntimeName(input); got != "github-copilot" {
			t.Fatalf("normalizeRuntimeName(%q) = %q", input, got)
		}
	}
}
