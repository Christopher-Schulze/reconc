package agentsession

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNormalizeKimiCodePayloadCoversEveryDocumentedRoute(t *testing.T) {
	repo := t.TempDir()
	toolEvents := map[string]bool{
		"PreToolUse": true, "PermissionRequest": true, "PermissionResult": true,
		"PostToolUse": true, "PostToolUseFailure": true,
	}
	for _, binding := range kimiCodeNativeEvents.entries() {
		route := binding.route
		nativeEvent := binding.primary
		t.Run(route, func(t *testing.T) {
			payload := fmt.Sprintf(`{"hook_event_name":%q,"session_id":"kimi-s1","cwd":%q`, nativeEvent, repo)
			if toolEvents[nativeEvent] {
				payload += `,"tool_name":"Write","tool_input":{"path":"docs/x.md"},"tool_call_id":"call-1"`
			}
			if nativeEvent == "PostToolUse" {
				payload += `,"tool_output":"ok"`
			}
			if nativeEvent == "PostToolUseFailure" {
				payload += `,"error":{"code":"tool_error","message":"failed","retryable":false}`
			}
			payload += "}"
			body, err := NormalizeKimiCodePayload(route, []byte(payload), repo)
			if err != nil {
				t.Fatalf("NormalizeKimiCodePayload: %v", err)
			}
			var normalized map[string]json.RawMessage
			if err := json.Unmarshal(body, &normalized); err != nil {
				t.Fatalf("decode normalized payload: %v", err)
			}
			if string(normalized["reconc_runtime"]) != `"kimi-code"` {
				t.Fatalf("reconc_runtime = %s", normalized["reconc_runtime"])
			}
			if nativeEvent == "Interrupt" && string(normalized["is_interrupt"]) != "true" {
				t.Fatalf("Interrupt is_interrupt = %s", normalized["is_interrupt"])
			}
		})
	}
}

func TestNormalizeKimiCodePayloadMapsToolEvidenceWithoutInventingExitStatus(t *testing.T) {
	repo := t.TempDir()
	payload := fmt.Sprintf(`{
		"hook_event_name":"PostToolUse",
		"session_id":"kimi-s1",
		"cwd":%q,
		"tool_name":"Bash",
		"tool_input":{"command":"go test ./..."},
		"tool_call_id":"call-1",
		"tool_output":"ok"
	}`, repo)
	body, err := NormalizeKimiCodePayload("kimi-post-tool-use", []byte(payload), repo)
	if err != nil {
		t.Fatalf("NormalizeKimiCodePayload: %v", err)
	}
	parsed, err := ParsePayload(body)
	if err != nil {
		t.Fatalf("ParsePayload: %v", err)
	}
	if parsed.ToolName != "Bash" || parsed.Command() != "go test ./..." || parsed.ToolUseID != "call-1" {
		t.Fatalf("unexpected normalized payload: %#v", parsed)
	}
	if parsed.ExitCode() != nil {
		t.Fatalf("Kimi Code does not publish an exit status; got %d", *parsed.ExitCode())
	}
}

func TestNormalizeKimiCodePayloadRejectsUnsafeShapes(t *testing.T) {
	repo := t.TempDir()
	outside := t.TempDir()
	tests := []struct {
		name  string
		route string
		body  string
		want  string
	}{
		{
			name: "route mismatch", route: "kimi-stop",
			body: fmt.Sprintf(`{"hook_event_name":"PreToolUse","session_id":"s1","cwd":%q}`, repo),
			want: "does not match route",
		},
		{
			name: "cwd escape", route: "kimi-stop",
			body: fmt.Sprintf(`{"hook_event_name":"Stop","session_id":"s1","cwd":%q}`, outside),
			want: "outside repository root",
		},
		{
			name: "missing input", route: "kimi-pre-tool-use",
			body: fmt.Sprintf(`{"hook_event_name":"PreToolUse","session_id":"s1","cwd":%q,"tool_name":"Write"}`, repo),
			want: "missing tool_input",
		},
		{
			name: "non-object input", route: "kimi-pre-tool-use",
			body: fmt.Sprintf(`{"hook_event_name":"PreToolUse","session_id":"s1","cwd":%q,"tool_name":"Write","tool_input":"x"}`, repo),
			want: "must be a JSON object",
		},
		{
			name: "trailing value", route: "kimi-stop",
			body: fmt.Sprintf(`{"hook_event_name":"Stop","session_id":"s1","cwd":%q} {}`, repo),
			want: "multiple JSON values",
		},
		{name: "unknown route", route: "kimi-unknown", body: `{}`, want: "unsupported Kimi Code hook route"},
		{name: "empty payload", route: "kimi-stop", body: ``, want: "empty Kimi Code payload"},
		{name: "invalid JSON", route: "kimi-stop", body: `{`, want: "unbalanced JSON braces"},
		{
			name: "missing session", route: "kimi-stop",
			body: fmt.Sprintf(`{"hook_event_name":"Stop","cwd":%q}`, repo),
			want: "missing non-empty session_id",
		},
		{
			name: "missing tool name", route: "kimi-post-tool-use",
			body: fmt.Sprintf(`{"hook_event_name":"PostToolUse","session_id":"s1","cwd":%q}`, repo),
			want: "missing tool_name",
		},
		{
			name: "invalid error", route: "kimi-post-tool-use-failure",
			body: fmt.Sprintf(`{"hook_event_name":"PostToolUseFailure","session_id":"s1","cwd":%q,"tool_name":"Write","error":[]}`, repo),
			want: "decode Kimi Code error",
		},
		{
			name: "empty error object", route: "kimi-post-tool-use-failure",
			body: fmt.Sprintf(`{"hook_event_name":"PostToolUseFailure","session_id":"s1","cwd":%q,"tool_name":"Write","error":{}}`, repo),
			want: "missing code or message",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := NormalizeKimiCodePayload(test.route, []byte(test.body), repo)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want substring %q", err, test.want)
			}
		})
	}
}

func TestNormalizeKimiCodePayloadAcceptsResolvedSubdirectory(t *testing.T) {
	repo := t.TempDir()
	child := filepath.Join(repo, "nested")
	if err := os.Mkdir(child, 0o755); err != nil {
		t.Fatal(err)
	}
	body := fmt.Sprintf(`{"hook_event_name":"Stop","session_id":"s1","cwd":%q}`, child)
	if _, err := NormalizeKimiCodePayload("kimi-stop", []byte(body), repo); err != nil {
		t.Fatalf("NormalizeKimiCodePayload: %v", err)
	}
}

func TestAdaptKimiCodeResultUsesExactBlockingExitContract(t *testing.T) {
	result := AdaptKimiCodeResult("kimi-stop", Result{
		Stdout: `{"decision":"block","reason":"finish the task"}`,
	})
	if result.ExitCode != 2 || result.Stderr != "finish the task" || result.Stdout != "" {
		t.Fatalf("adapted result = %#v", result)
	}

	result = AdaptKimiCodeResult("kimi-pre-tool-use", Result{ExitCode: 2, Stderr: "denied"})
	if result.ExitCode != 2 || result.Stderr != "denied" {
		t.Fatalf("pre-tool result = %#v", result)
	}

	result = AdaptKimiCodeResult("kimi-post-tool-use", Result{Stdout: "ignored observation"})
	if result.ExitCode != 0 || result.Stdout != "ignored observation" {
		t.Fatalf("observation result = %#v", result)
	}
}

func TestKimiCodeNormalizationAndAdaptationEdgeContracts(t *testing.T) {
	repo := t.TempDir()
	tests := []struct {
		name string
		body string
		want string
	}{
		{
			name: "missing cwd",
			body: `{"hook_event_name":"Stop","session_id":"s1"}`,
			want: "missing non-empty cwd",
		},
		{
			name: "missing repository",
			body: fmt.Sprintf(`{"hook_event_name":"Stop","session_id":"s1","cwd":%q}`, repo),
			want: "resolve Kimi Code repository root",
		},
		{
			name: "missing cwd path",
			body: fmt.Sprintf(`{"hook_event_name":"Stop","session_id":"s1","cwd":%q}`, filepath.Join(repo, "missing")),
			want: "resolve Kimi Code cwd",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := repo
			if test.name == "missing repository" {
				root = filepath.Join(repo, "missing-repo")
			}
			if _, err := NormalizeKimiCodePayload("kimi-stop", []byte(test.body), root); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want %q", err, test.want)
			}
		})
	}

	stringError := fmt.Sprintf(
		`{"hook_event_name":"PostToolUseFailure","session_id":"s1","cwd":%q,"tool_name":"Bash","error":" failed "}`,
		repo,
	)
	normalized, err := NormalizeKimiCodePayload("kimi-post-tool-use-failure", []byte(stringError), repo)
	if err != nil {
		t.Fatal(err)
	}
	if parsed, err := ParsePayload(normalized); err != nil || parsed.Error != "failed" {
		t.Fatalf("normalized string error = %#v, %v", parsed, err)
	}

	codeError := fmt.Sprintf(
		`{"hook_event_name":"PostToolUseFailure","session_id":"s1","cwd":%q,"tool_name":"Bash","error":{"code":"E_FAIL"}}`,
		repo,
	)
	normalized, err = NormalizeKimiCodePayload("kimi-post-tool-use-failure", []byte(codeError), repo)
	if err != nil {
		t.Fatal(err)
	}
	if parsed, err := ParsePayload(normalized); err != nil || parsed.Error != "E_FAIL" {
		t.Fatalf("normalized code error = %#v, %v", parsed, err)
	}

	blocked := AdaptKimiCodeResult("kimi-stop", Result{ExitCode: 7})
	if blocked.ExitCode != 2 || blocked.Stderr != "Reconc blocked this Kimi Code operation" || blocked.Stdout != "" {
		t.Fatalf("default block adaptation = %#v", blocked)
	}
	empty := AdaptKimiCodeResult("kimi-stop", Result{})
	if empty.ExitCode != 0 || empty.Stdout != "" || empty.Stderr != "" {
		t.Fatalf("empty control response = %#v", empty)
	}
	invalid := AdaptKimiCodeResult("kimi-stop", Result{Stdout: `{"decision":"allow"}`})
	if invalid.ExitCode != 2 || !strings.Contains(invalid.Stderr, "invalid Kimi Code control response") {
		t.Fatalf("invalid control response = %#v", invalid)
	}
}
