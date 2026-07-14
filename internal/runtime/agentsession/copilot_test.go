package agentsession

import (
	"encoding/json"
	"testing"
)

func TestNormalizeCopilotPayload(t *testing.T) {
	body, err := NormalizeCopilotPayload("copilot-post-tool-use", []byte(`{
  "hook_event_name":"PostToolUse",
  "session_id":"copilot-s1",
  "tool_name":"Bash",
  "tool_input":{"command":"go test ./..."},
  "tool_result":{"result_type":"success","exit_code":0}
}`))
	if err != nil {
		t.Fatal(err)
	}
	payload, err := ParsePayload(body)
	if err != nil {
		t.Fatal(err)
	}
	if payload.SessionID != "copilot-s1" || payload.ToolName != "Bash" || payload.Command() != "go test ./..." {
		t.Fatalf("unexpected normalized payload: %#v", payload)
	}
	if exitCode := payload.ExitCode(); exitCode == nil || *exitCode != 0 {
		t.Fatalf("tool_result was not normalized: %#v", payload.ToolResponse)
	}
}

func TestAdaptCopilotBlockingResults(t *testing.T) {
	pre := AdaptCopilotResult("copilot-pre-tool-use", Result{ExitCode: 2, Stderr: "blocked path"})
	var preJSON map[string]interface{}
	if err := json.Unmarshal([]byte(pre.Stdout), &preJSON); err != nil {
		t.Fatal(err)
	}
	if preJSON["permissionDecision"] != "deny" || pre.ExitCode != 0 {
		t.Fatalf("unexpected Copilot pre result: %#v", pre)
	}

	permission := AdaptCopilotResult("copilot-permission-request", Result{Stdout: permissionRequestDenyJSONOutput("not allowed")})
	var permissionJSON map[string]interface{}
	if err := json.Unmarshal([]byte(permission.Stdout), &permissionJSON); err != nil {
		t.Fatal(err)
	}
	if permissionJSON["behavior"] != "deny" || permissionJSON["message"] != "not allowed" {
		t.Fatalf("unexpected Copilot permission result: %#v", permissionJSON)
	}
}

func TestAdaptCopilotAdditionalContext(t *testing.T) {
	internal := `{"hookSpecificOutput":{"hookEventName":"PostToolUseFailure","additionalContext":"run tests again"}}`
	result := AdaptCopilotResult("copilot-post-tool-use-failure", Result{Stdout: internal})
	var body map[string]interface{}
	if err := json.Unmarshal([]byte(result.Stdout), &body); err != nil {
		t.Fatal(err)
	}
	if body["additionalContext"] != "run tests again" {
		t.Fatalf("unexpected context result: %#v", body)
	}
}
