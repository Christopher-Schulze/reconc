package agentsession

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestNormalizeDevinPayload(t *testing.T) {
	repo := filepath.Join(t.TempDir(), "repo")
	body, err := NormalizeDevinPayload("devin-pre-tool-use", []byte(`{
  "source":"devin-cli",
  "tool_name":"exec",
  "tool_input":{"command":"go test ./..."}
}`), repo)
	if err != nil {
		t.Fatal(err)
	}
	payload, err := ParsePayload(body)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(payload.SessionID, "devin-") || payload.ToolName != "Bash" || payload.Command() != "go test ./..." {
		t.Fatalf("unexpected normalized payload: %#v", payload)
	}
	if payload.Raw["reconc_runtime"] != "devin" || payload.Raw["devin_event"] != "devin-pre-tool-use" {
		t.Fatalf("missing Devin markers: %#v", payload.Raw)
	}
}

func TestNormalizeDevinToolCoverage(t *testing.T) {
	tests := []struct {
		name string
		want string
	}{
		{name: "read", want: "Read"},
		{name: "grep", want: "Read"},
		{name: "glob", want: "Read"},
		{name: "edit", want: "Write"},
		{name: "mcp__github__create_issue", want: "mcp__github__create_issue"},
	}
	for _, test := range tests {
		body, err := NormalizeDevinPayload("devin-post-tool-use", []byte(`{"session_id":"s1","tool_name":"`+test.name+`"}`), "/repo")
		if err != nil {
			t.Fatalf("%s: %v", test.name, err)
		}
		payload, err := ParsePayload(body)
		if err != nil {
			t.Fatal(err)
		}
		if payload.ToolName != test.want {
			t.Fatalf("%s mapped to %s, want %s", test.name, payload.ToolName, test.want)
		}
	}
}

func TestNormalizeDevinSuccessfulStderrRemainsDiagnosticOutput(t *testing.T) {
	body, err := NormalizeDevinPayload("devin-post-tool-use", []byte(`{
  "session_id":"s1",
  "tool_name":"exec",
  "tool_response":{"success":true,"output":"ok"},
  "stderr":"warning: cache miss"
}`), "/repo")
	if err != nil {
		t.Fatal(err)
	}
	payload, err := ParsePayload(body)
	if err != nil {
		t.Fatal(err)
	}
	if payload.Error != "" {
		t.Fatalf("successful stderr must not become a command failure: %q", payload.Error)
	}
	if payload.ToolResponse["stderr"] != "warning: cache miss" {
		t.Fatalf("stderr diagnostic was not preserved: %#v", payload.ToolResponse)
	}
}

func TestPayloadLooksLikeDevin(t *testing.T) {
	if !PayloadLooksLikeDevin([]byte(`{"source":"devin-cli"}`)) {
		t.Fatal("source marker should identify Devin")
	}
	if PayloadLooksLikeDevin([]byte(`{"source":"cursor"}`)) {
		t.Fatal("Cursor must not identify as Devin")
	}
	t.Setenv("DEVIN_PROJECT_DIR", "/repo")
	if !PayloadLooksLikeDevin([]byte(`{"source":"claude"}`)) {
		t.Fatal("DEVIN_PROJECT_DIR should suppress compatible Claude duplicates")
	}
}
