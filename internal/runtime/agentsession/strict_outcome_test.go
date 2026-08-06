package agentsession

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestStrictCommandOutcome(t *testing.T) {
	tests := []struct {
		name        string
		response    map[string]interface{}
		topError    string
		wantSuccess bool
	}{
		{name: "exit zero", response: map[string]interface{}{"exit_code": json.Number("0"), "success": true}, wantSuccess: true},
		{name: "successful stderr", response: map[string]interface{}{"exit_code": json.Number("0"), "success": true, "stderr": "warning"}, wantSuccess: true},
		{name: "exit one", response: map[string]interface{}{"exit_code": json.Number("1"), "success": false}},
		{name: "negative exit", response: map[string]interface{}{"exit_code": json.Number("-1"), "success": false}},
		{name: "missing exit", response: map[string]interface{}{"success": true}},
		{name: "fractional exit", response: map[string]interface{}{"exit_code": json.Number("1.5")}},
		{name: "overflowing exit", response: map[string]interface{}{"exit_code": json.Number("1e100")}},
		{name: "numeric string", response: map[string]interface{}{"exit_code": "1"}},
		{name: "boolean exit", response: map[string]interface{}{"exit_code": true}},
		{name: "object exit", response: map[string]interface{}{"exit_code": map[string]interface{}{"value": 1}}},
		{name: "conflicting aliases", response: map[string]interface{}{"exit_code": json.Number("0"), "exitCode": json.Number("1")}},
		{name: "conflicting success", response: map[string]interface{}{"exit_code": json.Number("1"), "success": true}},
		{name: "explicit response error", response: map[string]interface{}{"exit_code": json.Number("0"), "error": "failed"}},
		{name: "explicit host error", response: map[string]interface{}{"exit_code": json.Number("0")}, topError: "aborted"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			payload := &HookPayload{ToolResponse: test.response, Error: test.topError}
			got, diagnostic := strictCommandOutcome(payload)
			if got != test.wantSuccess {
				t.Fatalf("strictCommandOutcome() success = %v, want %v; diagnostic=%q", got, test.wantSuccess, diagnostic)
			}
			if !got && diagnostic == "" {
				t.Fatal("failed outcome has no diagnostic")
			}
		})
	}
}

func TestRunPostToolUseCompleteStrictRecordsAuthoritativeOutcomes(t *testing.T) {
	t.Setenv(StateRootEnv, t.TempDir())
	repo := setupPolicyRepo(t)
	if result := RunSessionStart(repo, []byte(`{"session_id":"strict-outcomes"}`)); result.ExitCode != 0 {
		t.Fatalf("session start = %+v", result)
	}
	tests := []struct {
		name    string
		body    string
		outcome string
	}{
		{name: "success", body: `{"session_id":"strict-outcomes","tool_name":"Bash","tool_input":{"command":"go test ./..."},"tool_response":{"exit_code":0,"success":true},"tool_use_id":"success-1"}`, outcome: "success"},
		{name: "failure", body: `{"session_id":"strict-outcomes","tool_name":"Bash","tool_input":{"command":"go test ./..."},"tool_response":{"exit_code":1,"success":false},"tool_use_id":"failure-1"}`, outcome: "failure"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result := RunPostToolUseCompleteStrict(repo, []byte(test.body))
			if result.ExitCode != 0 {
				t.Fatalf("strict post-tool result = %+v", result)
			}
			state, err := LoadSessionState(repo, "strict-outcomes")
			if err != nil {
				t.Fatal(err)
			}
			if len(state.CommandResults) == 0 || state.CommandResults[len(state.CommandResults)-1].Outcome != test.outcome {
				t.Fatalf("command results = %+v, want latest outcome %q", state.CommandResults, test.outcome)
			}
		})
	}
}

func TestRunPassiveEventIsFailOpenAndObservationOnly(t *testing.T) {
	t.Setenv(StateRootEnv, t.TempDir())
	repo := setupPolicyRepo(t)
	if result := RunPassiveEvent(repo, []byte(`{"session_id":"passive"}`)); result.ExitCode != 0 || result.Stdout != "" {
		t.Fatalf("passive event = %+v", result)
	}
	state, err := LoadSessionState(repo, "passive")
	if err != nil {
		t.Fatal(err)
	}
	if len(state.WritePaths) != 0 || len(state.ReadPaths) != 0 || len(state.Commands) != 0 {
		t.Fatalf("passive event changed evidence: %+v", state)
	}
	malformed := RunPassiveEvent(repo, []byte(`{"session_id":`))
	if malformed.ExitCode != 0 || !strings.Contains(malformed.Stderr, "passive") {
		t.Fatalf("malformed passive event = %+v", malformed)
	}
}
