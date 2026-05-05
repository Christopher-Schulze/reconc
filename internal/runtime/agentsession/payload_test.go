package agentsession

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

func TestReadPayloadUnderLimit(t *testing.T) {
	data, err := ReadPayload(strings.NewReader(`{"session_id":"s1"}`))
	if err != nil {
		t.Fatalf("ReadPayload: %v", err)
	}
	if len(data) == 0 {
		t.Error("expected non-empty payload")
	}
}

func TestReadPayloadRejectsOversizePayload(t *testing.T) {
	// 1 MiB + 1 byte of padding.
	big := make([]byte, MaxPayloadBytes+100)
	for i := range big {
		big[i] = 'x'
	}
	_, err := ReadPayload(bytes.NewReader(big))
	if !errors.Is(err, ErrPayloadTooLarge) {
		t.Errorf("expected ErrPayloadTooLarge, got %v", err)
	}
}

func TestParsePayloadEmptyErrors(t *testing.T) {
	_, err := ParsePayload(nil)
	if err == nil {
		t.Error("expected error for empty payload")
	}
}

func TestParsePayloadRequiresSessionID(t *testing.T) {
	_, err := ParsePayload([]byte(`{"tool_name":"Write"}`))
	if err == nil {
		t.Fatal("expected error for missing session_id")
	}
	if !strings.Contains(err.Error(), "session_id") {
		t.Errorf("error should mention session_id: %v", err)
	}
}

func TestParsePayloadRejectsWhitespaceOnlySessionID(t *testing.T) {
	_, err := ParsePayload([]byte(`{"session_id":"   "}`))
	if err == nil {
		t.Fatal("expected error for whitespace-only session_id")
	}
}

func TestParsePayloadHappyPath(t *testing.T) {
	raw := `{
		"session_id": "sess-abc",
		"tool_name": "Write",
		"tool_input": {"file_path": "src/main.go"},
		"tool_use_id": "use-123"
	}`
	p, err := ParsePayload([]byte(raw))
	if err != nil {
		t.Fatalf("ParsePayload: %v", err)
	}
	if p.SessionID != "sess-abc" || p.ToolName != "Write" {
		t.Errorf("fields wrong: %+v", p)
	}
	if p.FilePath() != "src/main.go" {
		t.Errorf("FilePath wrong: %q", p.FilePath())
	}
	if !p.IsWriteTool() {
		t.Errorf("IsWriteTool should be true for Write tool")
	}
}

func TestParsePayloadTrimsFields(t *testing.T) {
	raw := `{"session_id":"  s1  ","tool_input":{"file_path":"  docs/x.md  "}}`
	p, _ := ParsePayload([]byte(raw))
	if p.SessionID != "s1" {
		t.Errorf("session_id not trimmed: %q", p.SessionID)
	}
	if p.FilePath() != "docs/x.md" {
		t.Errorf("file_path not trimmed: %q", p.FilePath())
	}
}

func TestParsePayloadExitCodeVariants(t *testing.T) {
	for _, key := range []string{"exit_code", "exitCode", "status_code", "statusCode"} {
		raw := `{"session_id":"s1","tool_response":{"` + key + `":42}}`
		p, err := ParsePayload([]byte(raw))
		if err != nil {
			t.Fatalf("%s: %v", key, err)
		}
		ec := p.ExitCode()
		if ec == nil || *ec != 42 {
			t.Errorf("%s: expected exit_code 42, got %v", key, ec)
		}
	}
}

func TestParsePayloadNoExitCode(t *testing.T) {
	p, _ := ParsePayload([]byte(`{"session_id":"s1"}`))
	if p.ExitCode() != nil {
		t.Error("expected nil exit_code when absent")
	}
}

func TestParsePayloadRejectsDeepNesting(t *testing.T) {
	var b strings.Builder
	b.WriteString(`{"session_id":"s1","a":`)
	// 40 levels of {"n":{ -- past our 32 limit.
	for i := 0; i < 40; i++ {
		b.WriteString(`{"n":`)
	}
	b.WriteString(`1`)
	for i := 0; i < 40; i++ {
		b.WriteString(`}`)
	}
	b.WriteString(`}`)
	_, err := ParsePayload([]byte(b.String()))
	if err == nil {
		t.Fatal("expected depth-limit error")
	}
	if !strings.Contains(err.Error(), "levels of JSON nesting") {
		t.Errorf("expected depth error, got %v", err)
	}
}

func TestParsePayloadRejectsMalformedJSON(t *testing.T) {
	_, err := ParsePayload([]byte(`{not json`))
	if err == nil {
		t.Fatal("expected parse error")
	}
}

func TestParsePayloadRejectsUnbalancedBraces(t *testing.T) {
	_, err := ParsePayload([]byte(`{"session_id":"s1"`))
	if err == nil {
		t.Error("expected error for unbalanced JSON braces")
	}
}

func TestParsePayloadIsWriteTool(t *testing.T) {
	for _, name := range []string{"Edit", "MultiEdit", "Write"} {
		raw := `{"session_id":"s1","tool_name":"` + name + `"}`
		p, _ := ParsePayload([]byte(raw))
		if !p.IsWriteTool() {
			t.Errorf("%s should be a write tool", name)
		}
	}
	// Negative case.
	p, _ := ParsePayload([]byte(`{"session_id":"s1","tool_name":"Read"}`))
	if p.IsWriteTool() {
		t.Error("Read should not be a write tool")
	}
	if !p.IsReadTool() {
		t.Error("Read should be a read tool")
	}
}

func TestParsePayloadCommandTool(t *testing.T) {
	p, _ := ParsePayload([]byte(`{"session_id":"s1","tool_name":"Bash","tool_input":{"command":"ls"}}`))
	if !p.IsCommandTool() {
		t.Error("Bash should be a command tool")
	}
	if p.Command() != "ls" {
		t.Errorf("Command() wrong: %q", p.Command())
	}
}

func TestParsePayloadStopHookActive(t *testing.T) {
	p, _ := ParsePayload([]byte(`{"session_id":"s1","stop_hook_active":true}`))
	if !p.StopHookActive {
		t.Error("expected StopHookActive=true")
	}
	p2, _ := ParsePayload([]byte(`{"session_id":"s1"}`))
	if p2.StopHookActive {
		t.Error("expected StopHookActive=false when absent")
	}
}
