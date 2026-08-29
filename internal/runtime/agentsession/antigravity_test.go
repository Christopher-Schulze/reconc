package agentsession

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestNormalizeAntigravityPayloadPreToolUseWrite(t *testing.T) {
	body, err := NormalizeAntigravityPayload("antigravity-pre-tool-use", []byte(`{
		"conversationId":"ag-1",
		"stepIdx":7,
		"toolCall":{"name":"replace_file_content","args":{"TargetFile":"src/app.go"}}
	}`))
	if err != nil {
		t.Fatalf("NormalizeAntigravityPayload: %v", err)
	}
	payload, err := ParsePayload(body)
	if err != nil {
		t.Fatalf("ParsePayload: %v", err)
	}
	if payload.SessionID != "ag-1" || payload.ToolUseID != "step:7" {
		t.Fatalf("unexpected identity: session=%q tool=%q", payload.SessionID, payload.ToolUseID)
	}
	if !payload.IsWriteTool() || payload.FilePath() != "src/app.go" {
		t.Fatalf("expected write to src/app.go, got name=%q path=%q", payload.ToolName, payload.FilePath())
	}
}

func TestNormalizeAntigravityPayloadRunCommand(t *testing.T) {
	body, err := NormalizeAntigravityPayload("antigravity-pre-tool-use", []byte(`{
		"conversationId":"ag-2",
		"stepIdx":8,
		"toolCall":{"name":"run_command","args":{"CommandLine":"go test ./...","Cwd":"/repo"}}
	}`))
	if err != nil {
		t.Fatalf("NormalizeAntigravityPayload: %v", err)
	}
	payload, err := ParsePayload(body)
	if err != nil {
		t.Fatalf("ParsePayload: %v", err)
	}
	if !payload.IsCommandTool() || payload.Command() != "go test ./..." {
		t.Fatalf("expected command tool, got name=%q command=%q", payload.ToolName, payload.Command())
	}
}

func TestAntigravityPrePostToolRecordsPendingEvidence(t *testing.T) {
	repo := setupPolicyRepo(t)
	pre := RunAntigravityPreToolUse(repo, []byte(`{
		"conversationId":"ag-3",
		"stepIdx":9,
		"toolCall":{"name":"view_file","args":{"AbsolutePath":"docs/tasks.md"}}
	}`))
	if pre.ExitCode != 0 || !strings.Contains(pre.Stdout, `"decision":"allow"`) {
		t.Fatalf("pre should allow, exit=%d stdout=%s stderr=%s", pre.ExitCode, pre.Stdout, pre.Stderr)
	}
	post := RunAntigravityPostToolUse(repo, []byte(`{
		"conversationId":"ag-3",
		"stepIdx":9,
		"error":""
	}`))
	if post.ExitCode != 0 || strings.TrimSpace(post.Stdout) != "{}" {
		t.Fatalf("post should return empty object, exit=%d stdout=%s stderr=%s", post.ExitCode, post.Stdout, post.Stderr)
	}
	state, err := LoadSessionState(repo, "ag-3")
	if err != nil {
		t.Fatalf("LoadSessionState: %v", err)
	}
	if len(state.ReadPaths) != 1 || state.ReadPaths[0] != "docs/tasks.md" {
		t.Fatalf("expected read evidence, got %+v", state.ReadPaths)
	}
	if len(state.PendingToolCalls) != 0 {
		t.Fatalf("pending tool call was not cleared: %+v", state.PendingToolCalls)
	}
}

func TestAntigravityPendingKeyWithoutStepIDUsesToolFingerprint(t *testing.T) {
	readDoc, err := NormalizeAntigravityPayload("antigravity-pre-tool-use", []byte(`{
		"conversationId":"ag-no-step",
		"toolCall":{"name":"view_file","args":{"AbsolutePath":"docs/tasks.md"}}
	}`))
	if err != nil {
		t.Fatalf("normalize docs read: %v", err)
	}
	readStart, err := NormalizeAntigravityPayload("antigravity-pre-tool-use", []byte(`{
		"conversationId":"ag-no-step",
		"toolCall":{"name":"view_file","args":{"AbsolutePath":"start.md"}}
	}`))
	if err != nil {
		t.Fatalf("normalize start read: %v", err)
	}
	docPayload, err := ParsePayload(readDoc)
	if err != nil {
		t.Fatalf("parse docs read: %v", err)
	}
	startPayload, err := ParsePayload(readStart)
	if err != nil {
		t.Fatalf("parse start read: %v", err)
	}
	docKey, err := antigravityPendingKey(docPayload)
	if err != nil {
		t.Fatal(err)
	}
	startKey, err := antigravityPendingKey(startPayload)
	if err != nil {
		t.Fatal(err)
	}
	if docKey == "step:unknown" || startKey == "step:unknown" {
		t.Fatalf("missing step id must still produce deterministic tool fingerprint keys, got %q and %q", docKey, startKey)
	}
	if docKey == startKey {
		t.Fatalf("different no-step tool calls must not collide, got %q", docKey)
	}
}

func TestAntigravityStopAdaptsBlockToContinue(t *testing.T) {
	result := AdaptAntigravityResult("antigravity-stop", Result{ExitCode: 0, Stdout: `{"decision":"block","reason":"fix it"}`})
	if result.ExitCode != 0 {
		t.Fatalf("adapt exit=%d", result.ExitCode)
	}
	var out map[string]string
	if err := json.Unmarshal([]byte(result.Stdout), &out); err != nil {
		t.Fatalf("stdout json: %v", err)
	}
	if out["decision"] != "continue" || out["reason"] != "fix it" {
		t.Fatalf("unexpected stop adaptation: %#v", out)
	}
}

func TestNormalizeAntigravityPayloadRejectsUnsafeShapes(t *testing.T) {
	for _, test := range []struct {
		name string
		body string
		want string
	}{
		{name: "empty", body: "", want: "empty"},
		{name: "malformed", body: "{", want: "unbalanced JSON"},
		{name: "null", body: "null", want: "JSON object"},
		{name: "too deep", body: strings.Repeat("[", MaxJSONDepth+1) + strings.Repeat("]", MaxJSONDepth+1), want: "nesting"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := NormalizeAntigravityPayload("antigravity-pre-tool-use", []byte(test.body)); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want substring %q", err, test.want)
			}
		})
	}
}

func TestNormalizeAntigravityPayloadCoversEveryToolMapping(t *testing.T) {
	tests := []struct {
		name     string
		tool     string
		args     string
		wantTool string
		wantPath string
	}{
		{name: "view", tool: "view_file", args: `"AbsolutePath":"a.go"`, wantTool: "Read", wantPath: "a.go"},
		{name: "write", tool: "write_to_file", args: `"TargetFile":"b.go"`, wantTool: "Write", wantPath: "b.go"},
		{name: "multi replace", tool: "multi_replace_file_content", args: `"file_path":"c.go"`, wantTool: "Write", wantPath: "c.go"},
		{name: "list", tool: "list_dir", args: `"DirectoryPath":"docs"`, wantTool: "Read", wantPath: "docs"},
		{name: "find", tool: "find_by_name", args: `"SearchDirectory":"internal"`, wantTool: "Read", wantPath: "internal"},
		{name: "grep", tool: "grep_search", args: `"SearchPath":"scripts"`, wantTool: "Read", wantPath: "scripts"},
		{name: "unknown", tool: "custom_tool", args: `"path":"custom"`, wantTool: "custom_tool"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			raw := `{"session_id":"ag","step_idx":2,"termination_reason":"user abort","tool_call":{"name":"` + test.tool + `","arguments":{` + test.args + `}}}`
			body, err := NormalizeAntigravityPayload("antigravity-pre-tool-use", []byte(raw))
			if err != nil {
				t.Fatal(err)
			}
			payload, err := ParsePayload(body)
			if err != nil {
				t.Fatal(err)
			}
			if payload.SessionID != "ag" || payload.ToolUseID != "step:2" || payload.IsInterrupt == nil || !*payload.IsInterrupt {
				t.Fatalf("identity normalization = %+v", payload)
			}
			if payload.ToolName != test.wantTool {
				t.Fatalf("tool = %q, want %q", payload.ToolName, test.wantTool)
			}
			if test.wantPath != "" && payload.FilePath() != test.wantPath {
				t.Fatalf("path = %q, want %q", payload.FilePath(), test.wantPath)
			}
		})
	}
	body, err := NormalizeAntigravityPayload("antigravity-post-tool-use", []byte(`{"error":" failed "}`))
	if err != nil {
		t.Fatal(err)
	}
	payload, err := ParsePayload(body)
	if err != nil || payload.SessionID != "antigravity-workspace" || payload.Error != "failed" {
		t.Fatalf("fallback normalization = %+v, %v", payload, err)
	}
}

func TestAdaptAntigravityResultCoversHostProtocolBranches(t *testing.T) {
	tests := []struct {
		name       string
		event      string
		input      Result
		wantOutput string
		wantStderr string
	}{
		{name: "pre deny stderr", event: "antigravity-pre-tool-use", input: Result{ExitCode: 2, Stderr: "denied"}, wantOutput: `"decision":"deny"`, wantStderr: ""},
		{name: "pre deny stdout", event: "antigravity-pre-tool-use", input: Result{ExitCode: 2, Stdout: "reason"}, wantOutput: `"reason":"reason"`},
		{name: "pre deny fallback", event: "antigravity-pre-tool-use", input: Result{ExitCode: 2}, wantOutput: `"reason":"reconc denied`},
		{name: "pre allow", event: "antigravity-pre-tool-use", input: Result{Stderr: "warning"}, wantOutput: `"decision":"allow"`, wantStderr: "warning"},
		{name: "post", event: "antigravity-post-tool-use", input: Result{ExitCode: 2, Stderr: "warning"}, wantOutput: `{}`, wantStderr: "warning"},
		{name: "stop denied", event: "antigravity-stop", input: Result{ExitCode: 2, Stderr: "continue"}, wantOutput: `"decision":"continue"`},
		{name: "stop plain", event: "antigravity-stop", input: Result{Stdout: "unfinished"}, wantOutput: `"reason":"unfinished"`},
		{name: "stop", event: "antigravity-stop", input: Result{}, wantOutput: `"decision":"stop"`},
		{name: "unknown empty", event: "unknown", input: Result{}, wantOutput: `{}`},
		{name: "unknown passthrough", event: "unknown", input: Result{ExitCode: 3, Stdout: "raw"}, wantOutput: "raw"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := AdaptAntigravityResult(test.event, test.input)
			if !strings.Contains(got.Stdout, test.wantOutput) || got.Stderr != test.wantStderr {
				t.Fatalf("result = %+v, want output %q and stderr %q", got, test.wantOutput, test.wantStderr)
			}
		})
	}
}

func TestAntigravityInvocationLifecycleIsFailOpenAndBounded(t *testing.T) {
	repo := setupPolicyRepo(t)
	for _, test := range []struct {
		name string
		run  func(string, []byte) Result
		body string
		want string
	}{
		{name: "pre invalid", run: RunAntigravityPreInvocation, body: "{", want: "injectSteps"},
		{name: "pre valid", run: RunAntigravityPreInvocation, body: `{"conversationId":"ag-lifecycle"}`, want: "injectSteps"},
		{name: "post invalid", run: RunAntigravityPostInvocation, body: "{", want: "terminationBehavior"},
		{name: "post valid", run: RunAntigravityPostInvocation, body: `{"conversationId":"ag-lifecycle"}`, want: "terminationBehavior"},
	} {
		t.Run(test.name, func(t *testing.T) {
			got := test.run(repo, []byte(test.body))
			if got.ExitCode != 0 || !strings.Contains(got.Stdout, test.want) {
				t.Fatalf("result = %+v", got)
			}
		})
	}
	if got := RunAntigravityStop(repo, []byte("{")); got.ExitCode != 0 || !strings.Contains(got.Stdout, `"decision":"continue"`) {
		t.Fatalf("invalid stop result = %+v", got)
	}
	if got := RunAntigravityPostToolUse(repo, []byte(`{"conversationId":"missing","stepIdx":99}`)); got.ExitCode != 0 || strings.TrimSpace(got.Stdout) != "{}" {
		t.Fatalf("post without pending call = %+v", got)
	}
}
