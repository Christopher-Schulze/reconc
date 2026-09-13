package agentsession

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"testing"
)

func TestNormalizeDevinPayload(t *testing.T) {
	repo := t.TempDir()
	body, err := NormalizeDevinPayload("devin-pre-tool-use", []byte(fmt.Sprintf(`{
  "hook_event_name":"PreToolUse",
  "cwd":%q,
  "source":"devin-cli",
	"session_id":"native-session",
	"prompt_id":"native-turn",
	"tool_use_id":"call-1",
  "tool_name":"exec",
  "tool_input":{"command":"go test ./..."}
}`, repo)), repo)
	if err != nil {
		t.Fatal(err)
	}
	payload, err := ParsePayload(body)
	if err != nil {
		t.Fatal(err)
	}
	identity := sha256.Sum256([]byte("native-turn\x00call-1"))
	if payload.SessionID != "native-session" || payload.ToolUseID != "devin-"+hex.EncodeToString(identity[:]) || payload.ToolName != "Bash" || payload.Command() != "go test ./..." {
		t.Fatalf("unexpected normalized payload: %#v", payload)
	}
	if payload.Raw["reconc_runtime"] != "devin" || payload.Raw["devin_event"] != "devin-pre-tool-use" || payload.Raw["prompt_id"] != "native-turn" {
		t.Fatalf("missing Devin markers: %#v", payload.Raw)
	}
}

func TestNormalizeDevinIdentityBoundary(t *testing.T) {
	repo := t.TempDir()
	t.Setenv("DEVIN_SESSION_ID", "shared-env-session")
	t.Setenv("DEVIN_PROJECT_DIR", repo)
	for _, test := range []struct {
		name string
		body string
	}{
		{name: "missing native session", body: `{"hook_event_name":"PreToolUse","prompt_id":"turn","tool_use_id":"call","tool_name":"write"}`},
		{name: "session alias", body: `{"hook_event_name":"PreToolUse","sessionId":"alias","prompt_id":"turn","tool_use_id":"call","tool_name":"write"}`},
		{name: "repository identity", body: `{"hook_event_name":"PreToolUse","project_id":"repo","prompt_id":"turn","tool_use_id":"call","tool_name":"write"}`},
		{name: "blank session", body: `{"hook_event_name":"PreToolUse","session_id":" ","prompt_id":"turn","tool_use_id":"call","tool_name":"write"}`},
		{name: "duplicate session", body: `{"hook_event_name":"PreToolUse","session_id":"first","session_id":"second","prompt_id":"turn","tool_use_id":"call","tool_name":"write"}`},
		{name: "wrong prompt type", body: `{"hook_event_name":"PreToolUse","session_id":"session","prompt_id":7,"tool_use_id":"call","tool_name":"write"}`},
		{name: "blank prompt", body: `{"hook_event_name":"PreToolUse","session_id":"session","prompt_id":"","tool_use_id":"call","tool_name":"write"}`},
		{name: "wrong call type", body: `{"hook_event_name":"PreToolUse","session_id":"session","prompt_id":"turn","tool_use_id":7,"tool_name":"write"}`},
		{name: "blank call", body: `{"hook_event_name":"PreToolUse","session_id":"session","prompt_id":"turn","tool_use_id":"","tool_name":"write"}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := NormalizeDevinPayload("devin-pre-tool-use", []byte(test.body), repo); err == nil {
				t.Fatal("unbound Devin identity was accepted")
			}
		})
	}
	start, err := NormalizeDevinPayload("devin-session-start", []byte(`{"hook_event_name":"SessionStart","session_id":"session"}`), repo)
	if err != nil {
		t.Fatalf("native SessionStart without prompt_id: %v", err)
	}
	if payload, err := ParsePayload(start); err != nil || payload.SessionID != "session" {
		t.Fatalf("SessionStart lost its native identity: %#v, %v", payload, err)
	}
	for _, test := range []struct {
		session string
		prompt  string
	}{
		{session: "session-a", prompt: "turn-1"},
		{session: "session-b", prompt: "turn-1"},
		{session: "session-a", prompt: "turn-2"},
	} {
		body := fmt.Sprintf(`{"hook_event_name":"PreToolUse","session_id":%q,"prompt_id":%q,"tool_use_id":"call-1","tool_name":"write","tool_input":{"file_path":"probe.txt"}}`, test.session, test.prompt)
		encoded, err := NormalizeDevinPayload("devin-pre-tool-use", []byte(body), repo)
		if err != nil {
			t.Fatal(err)
		}
		payload, err := ParsePayload(encoded)
		if err != nil || payload.SessionID != test.session {
			t.Fatalf("cross-session identity drift: %#v, %v", payload, err)
		}
		if test.prompt == "turn-1" {
			continue
		}
		first := sha256.Sum256([]byte("turn-1\x00call-1"))
		if payload.ToolUseID == "devin-"+hex.EncodeToString(first[:]) {
			t.Fatal("reused call ID replayed across native turns")
		}
	}
}

func TestNormalizeDevinNativeProjectBinding(t *testing.T) {
	repo := t.TempDir()
	foreign := t.TempDir()
	const native = `{"hook_event_name":"SessionStart","session_id":"native-session"}`
	t.Run("native environment without payload cwd", func(t *testing.T) {
		t.Setenv("DEVIN_PROJECT_DIR", repo)
		if _, err := NormalizeDevinPayload("devin-session-start", []byte(native), repo); err != nil {
			t.Fatalf("native Devin payload was rejected: %v", err)
		}
	})
	t.Run("missing project binding", func(t *testing.T) {
		t.Setenv("DEVIN_PROJECT_DIR", "")
		if _, err := NormalizeDevinPayload("devin-session-start", []byte(native), repo); err == nil {
			t.Fatal("unbound native Devin payload was accepted")
		}
	})
	t.Run("foreign project environment", func(t *testing.T) {
		t.Setenv("DEVIN_PROJECT_DIR", foreign)
		if _, err := NormalizeDevinPayload("devin-session-start", []byte(native), repo); err == nil {
			t.Fatal("foreign Devin project environment was accepted")
		}
	})
	t.Run("conflicting payload cwd", func(t *testing.T) {
		t.Setenv("DEVIN_PROJECT_DIR", repo)
		body := fmt.Sprintf(`{"hook_event_name":"SessionStart","session_id":"native-session","cwd":%q}`, foreign)
		if _, err := NormalizeDevinPayload("devin-session-start", []byte(body), repo); err == nil {
			t.Fatal("foreign payload cwd overrode native project binding")
		}
	})
}

func TestNormalizeDevinToolCoverage(t *testing.T) {
	repo := t.TempDir()
	tests := []struct {
		name string
		want string
	}{
		{name: "read", want: "Read"},
		{name: "grep", want: "Read"},
		{name: "glob", want: "Read"},
		{name: "edit", want: "Write"},
		{name: "write", want: "Write"},
		{name: "notebook_edit", want: "NotebookEdit"},
		{name: "apply_patch", want: "apply_patch"},
		{name: "mcp__github__create_issue", want: "mcp__github__create_issue"},
	}
	for _, test := range tests {
		raw := fmt.Sprintf(`{"hook_event_name":"PostToolUse","session_id":"s1","prompt_id":"turn-1","tool_use_id":"call-1","cwd":%q,"tool_name":%q,"tool_input":{}}`, repo, test.name)
		body, err := NormalizeDevinPayload("devin-post-tool-use", []byte(raw), repo)
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

func TestNormalizeDevinToolInputUsesNativeObjectOnly(t *testing.T) {
	repo := t.TempDir()
	t.Setenv("DEVIN_PROJECT_DIR", repo)
	for _, test := range []struct {
		name, body string
	}{
		{name: "missing native input", body: `{"toolInput":{"file_path":"docs/allowed.txt"}}`},
		{name: "invalid native input", body: `{"tool_input":null,"file_path":"docs/allowed.txt"}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			body := fmt.Sprintf(`{"hook_event_name":"PreToolUse","session_id":"s1","prompt_id":"turn-1","tool_use_id":"call-1","tool_name":"write",%s`, test.body[1:])
			if _, err := NormalizeDevinPayload("devin-pre-tool-use", []byte(body), repo); err == nil {
				t.Fatal("non-native tool input was accepted")
			}
		})
	}
	body := `{"hook_event_name":"PreToolUse","session_id":"s1","prompt_id":"turn-1","tool_use_id":"call-1","tool_name":"write","tool_input":{},"file_path":"docs/allowed.txt"}`
	normalized, err := NormalizeDevinPayload("devin-pre-tool-use", []byte(body), repo)
	if err != nil {
		t.Fatal(err)
	}
	payload, err := ParsePayload(normalized)
	if err != nil {
		t.Fatal(err)
	}
	if len(payload.FilePaths()) != 0 {
		t.Fatalf("top-level path changed native tool input: %v", payload.FilePaths())
	}
}

func TestNormalizeDevinSuccessfulStderrRemainsDiagnosticOutput(t *testing.T) {
	repo := t.TempDir()
	body, err := NormalizeDevinPayload("devin-post-tool-use", []byte(fmt.Sprintf(`{
  "hook_event_name":"PostToolUse",
  "cwd":%q,
  "session_id":"s1",
  "prompt_id":"turn-1",
  "tool_use_id":"call-1",
  "tool_name":"exec",
	"tool_input":{"command":"go test ./..."},
  "tool_response":{"success":true,"output":"ok"},
	"error":"forged outer error",
	"exit_code":7,
  "stderr":"warning: cache miss"
}`, repo)), repo)
	if err != nil {
		t.Fatal(err)
	}
	payload, err := ParsePayload(body)
	if err != nil {
		t.Fatal(err)
	}
	if payload.Error != "" {
		t.Fatalf("outer error or stderr must not become a command failure: %q", payload.Error)
	}
	if _, present := payload.Raw["exit_code"]; present {
		t.Fatal("outer exit status must not become authoritative shell outcome")
	}
	if payload.ToolResponse["stderr"] != "warning: cache miss" {
		t.Fatalf("stderr diagnostic was not preserved: %#v", payload.ToolResponse)
	}
}

func TestPayloadLooksLikeDevin(t *testing.T) {
	if !PayloadLooksLikeDevin([]byte(`{"source":"devin-cli"}`), t.TempDir()) {
		t.Fatal("source marker should identify Devin")
	}
	if PayloadLooksLikeDevin([]byte(`{"source":"cursor"}`), t.TempDir()) {
		t.Fatal("Cursor must not identify as Devin")
	}
	repo := t.TempDir()
	t.Setenv("DEVIN_PROJECT_DIR", repo)
	if !PayloadLooksLikeDevin([]byte(`{"source":"claude"}`), repo) {
		t.Fatal("DEVIN_PROJECT_DIR should suppress compatible Claude duplicates")
	}
	if PayloadLooksLikeDevin([]byte(`{"source":"claude"}`), t.TempDir()) {
		t.Fatal("a foreign DEVIN_PROJECT_DIR must not suppress this repository's route")
	}
}
