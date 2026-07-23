package agentsession

import (
	"encoding/json"
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
