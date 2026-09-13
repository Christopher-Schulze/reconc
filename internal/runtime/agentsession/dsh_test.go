package agentsession

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"reconc.dev/reconc/internal/policy"
)

func TestNormalizeDSHNativeToolAndObservationBoundaries(t *testing.T) {
	repo, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name, route, native, input, extra, wantTool, wantPath string
		wantMCP                                               bool
	}{
		{name: "native read", route: "dsh-pre-tool-use", native: "read", input: `{"file_path":"docs/read.md"}`, wantTool: "Read", wantPath: "docs/read.md", wantMCP: true},
		{name: "native write", route: "dsh-pre-tool-use", native: "write", input: `{"file_path":"generated/out.go","content":"candidate"}`, wantTool: "Write", wantPath: "generated/out.go", wantMCP: true},
		{name: "native edit", route: "dsh-pre-tool-use", native: "edit", input: `{"file_path":"generated/out.go","old_string":"x","new_string":"y"}`, wantTool: "Edit", wantPath: "generated/out.go", wantMCP: true},
		{name: "editor read", route: "dsh-pre-tool-use", native: "str_replace_editor", input: `{"command":"view","path":"docs/read.md"}`, wantTool: "Read", wantPath: "docs/read.md", wantMCP: true},
		{name: "editor create", route: "dsh-pre-tool-use", native: "str_replace_editor", input: `{"command":"create","path":"generated/out.go","file_text":"candidate"}`, wantTool: "Write", wantPath: "generated/out.go", wantMCP: true},
		{name: "bash", route: "dsh-pre-tool-use", native: "bash", input: `{"command":"go test ./...","description":"Test the project"}`, wantTool: "Bash", wantMCP: true},
		{name: "observed success", route: "dsh-post-tool-use", native: "str_replace_editor", input: `{"command":"create","path":"generated/out.go"}`, extra: `,"is_error":false,"result_observed":true`, wantTool: "Write", wantPath: "generated/out.go"},
		{name: "observed failure", route: "dsh-post-tool-use-failure", native: "bash", input: `{"command":"false"}`, extra: `,"is_error":true,"result_observed":true`, wantTool: "Bash"},
		{name: "compact editor observation", route: "dsh-post-tool-use", native: "str_replace_editor", input: `{}`, extra: `,"is_error":false,"result_observed":true`, wantTool: "str_replace_editor"},
		{name: "compact shell failure", route: "dsh-post-tool-use-failure", native: "bash", input: `{}`, extra: `,"is_error":true,"result_observed":true`, wantTool: "Bash"},
	} {
		t.Run(test.name, func(t *testing.T) {
			payload := fmt.Sprintf(`{"hook_event_name":%q,"session_id":"dsh-session","cwd":%q,"tool_name":%q,"tool_input":%s,"tool_call_id":"call-1","root_call_id":"root-1","agent_id":"agent-1"%s}`, map[string]string{"dsh-pre-tool-use": "tools/pre-execute", "dsh-post-tool-use": "tools/result", "dsh-post-tool-use-failure": "tools/result"}[test.route], repo, test.native, test.input, test.extra)
			body, err := NormalizeDSHPayload(test.route, []byte(payload), repo)
			if err != nil {
				t.Fatal(err)
			}
			parsed, err := ParsePayload(body)
			if err != nil {
				t.Fatal(err)
			}
			if parsed.SessionID != "dsh-session" || parsed.ToolUseID != "call-1" || parsed.ToolName != test.wantTool || parsed.FilePath() != test.wantPath {
				t.Fatalf("normalized DSH tool = %+v", parsed)
			}
			if test.wantMCP {
				if parsed.MCP == nil || parsed.MCP.Platform != policy.MCPPlatform("custom:dsh") || parsed.MCP.Tool != test.native || !parsed.MCP.BlockingPreHook {
					t.Fatalf("DSH pre MCP selector = %+v", parsed.MCP)
				}
			} else if parsed.MCP != nil {
				t.Fatalf("transformed post result created MCP effect evidence: %+v", parsed.MCP)
			}
			if parsed.Raw["dsh_root_call_id"] != "root-1" || parsed.Raw["dsh_agent_id"] != "agent-1" || parsed.Raw["reconc_runtime"] != "dsh" {
				t.Fatalf("DSH identity lost: %+v", parsed.Raw)
			}
		})
	}
	start := fmt.Sprintf(`{"hook_event_name":"session_start","session_id":"dsh-session","cwd":%q}`, repo)
	if _, err := NormalizeDSHPayload("dsh-session-start", []byte(start), repo); err != nil {
		t.Fatalf("session-start normalization: %v", err)
	}
	for _, active := range []bool{false, true} {
		stop := fmt.Sprintf(`{"hook_event_name":"agent/turn-stopping","session_id":"dsh-session","cwd":%q,"agent_id":"agent-1","stop_hook_active":%t}`, repo, active)
		body, err := NormalizeDSHPayload("dsh-stop", []byte(stop), repo)
		if err != nil {
			t.Fatalf("stop normalization: %v", err)
		}
		parsed, err := ParsePayload(body)
		if err != nil || parsed.StopHookActive != active || parsed.SessionID != "dsh-session" {
			t.Fatalf("normalized DSH stop = %+v, %v", parsed, err)
		}
	}
}

func TestNormalizeDSHRejectsUnboundOrMisleadingEvents(t *testing.T) {
	repo, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	valid := fmt.Sprintf(`{"hook_event_name":"tools/pre-execute","session_id":"s","cwd":%q,"tool_name":"str_replace_editor","tool_input":{"command":"create","path":"generated/out.go"},"tool_call_id":"c","root_call_id":"r"}`, repo)
	subdir := filepath.Join(repo, "nested")
	if err := os.Mkdir(subdir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name, route, payload, want string
	}{
		{name: "empty", route: "dsh-pre-tool-use", payload: "", want: "empty DSH payload"},
		{name: "unknown route", route: "dsh-unknown", payload: valid, want: "unsupported DSH hook route"},
		{name: "event mismatch", route: "dsh-post-tool-use", payload: valid, want: "does not match route"},
		{name: "missing root call", route: "dsh-pre-tool-use", payload: strings.Replace(valid, `"root_call_id":"r"`, `"root_call_id":""`, 1), want: "requires name, call ID, and root call ID"},
		{name: "nonobject input", route: "dsh-pre-tool-use", payload: strings.Replace(valid, `"tool_input":{"command":"create","path":"generated/out.go"}`, `"tool_input":"create"`, 1), want: "JSON object"},
		{name: "unknown edit command", route: "dsh-pre-tool-use", payload: strings.Replace(valid, `"command":"create"`, `"command":"undo_edit"`, 1), want: "unsupported DSH str_replace_editor command"},
		{name: "post lacks observed marker", route: "dsh-post-tool-use", payload: strings.Replace(valid, `"tools/pre-execute"`, `"tools/result"`, 1), want: "final result observation"},
		{name: "outside cwd", route: "dsh-pre-tool-use", payload: strings.Replace(valid, repo, filepath.Dir(repo), 1), want: "outside repository root"},
		{name: "subdirectory cwd", route: "dsh-pre-tool-use", payload: strings.Replace(valid, repo, subdir, 1), want: "session cwd must equal repository root"},
		{name: "stop missing active", route: "dsh-stop", payload: fmt.Sprintf(`{"hook_event_name":"agent/turn-stopping","session_id":"s","cwd":%q}`, repo), want: "requires stop_hook_active"},
		{name: "unsupported PowerShell", route: "dsh-pre-tool-use", payload: strings.Replace(strings.Replace(valid, `"tool_name":"str_replace_editor"`, `"tool_name":"pwsh"`, 1), `"tool_input":{"command":"create","path":"generated/out.go"}`, `"tool_input":{"command":"Write-Output test"}`, 1), want: "no Reconc command-policy parser"},
		{name: "bash outside workdir", route: "dsh-pre-tool-use", payload: strings.Replace(strings.Replace(valid, `"tool_name":"str_replace_editor"`, `"tool_name":"bash"`, 1), `"tool_input":{"command":"create","path":"generated/out.go"}`, fmt.Sprintf(`"tool_input":{"command":"pwd","workdir":%q}`, filepath.Dir(repo)), 1), want: "outside repository root"},
		{name: "bash subdirectory workdir", route: "dsh-pre-tool-use", payload: strings.Replace(strings.Replace(valid, `"tool_name":"str_replace_editor"`, `"tool_name":"bash"`, 1), `"tool_input":{"command":"create","path":"generated/out.go"}`, `"tool_input":{"command":"pwd","workdir":"nested"}`, 1), want: "bash workdir must equal repository root"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := NormalizeDSHPayload(test.route, []byte(test.payload), repo); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("normalization error = %v, want %q", err, test.want)
			}
		})
	}
}
