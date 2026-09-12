package agentsession

import (
	"encoding/json"
	"testing"
)

func TestCodexChildNormalizationPreservesOpaqueToolPayload(t *testing.T) {
	body := []byte(`{"session_id":"root-session","agent_id":"child-thread","tool_use_id":"call-1","tool_name":"mcp__files__write","tool_input":{"path":"docs/out.md","counter":9007199254740993,"content":"session_id is text"},"tool_response":{"isError":false}}`)
	normalized, err := NormalizeCodexPayload("codex-mcp-after", body)
	if err != nil {
		t.Fatal(err)
	}
	var got, original map[string]json.RawMessage
	if err := json.Unmarshal(normalized, &got); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(body, &original); err != nil {
		t.Fatal(err)
	}
	if string(got["session_id"]) != `"child-thread"` || len(got) != len(original) {
		t.Fatalf("wrong child identity or lost envelope fields: %s", normalized)
	}
	for field, value := range original {
		if field != "session_id" && string(got[field]) != string(value) {
			t.Fatalf("normalization changed opaque field %s: got=%s want=%s", field, got[field], value)
		}
	}
}

func TestCodexRootPayloadRetainsCompatibilityBytes(t *testing.T) {
	for _, body := range []string{`{ "session_id": "root", "tool_input": {} }`, `{}`, `{"sessionId":"legacy"}`} {
		got, err := NormalizeCodexPayload("codex-pre-tool-use", []byte(body))
		if err != nil || string(got) != body {
			t.Fatalf("root compatibility changed: got=%s error=%v", got, err)
		}
	}
}

func TestCodexChildNormalizationRejectsMalformedIdentity(t *testing.T) {
	for _, body := range []string{
		`{"session_id":"root","agent_id":null}`,
		`{"session_id":"root","agent_id":""}`,
		`{"session_id":"root","agent_id":" child "}`,
		`{"session_id":"root","agent_id":42}`,
		`{"session_id":"root","agent_id":"a","agent_id":"b"}`,
		`{"agent_id":"child"}`,
		`{"session_id":null,"agent_id":"child"}`,
		`{"session_id":" root ","agent_id":"child"}`,
		`{"session_id":"root","hook_event_name":"SubagentStart"}`,
		`{"session_id":"root","hook_event_name":"SubagentStop"}`,
	} {
		if normalized, err := NormalizeCodexPayload("codex-pre-tool-use", []byte(body)); err == nil || normalized != nil {
			t.Fatalf("invalid child identity accepted: %s -> %s error=%v", body, normalized, err)
		}
	}
}
