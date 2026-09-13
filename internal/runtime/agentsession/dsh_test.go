package agentsession

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"reconc.dev/reconc/internal/policy"
)

func TestAdaptDSHResultNeverVetoesDispatch(t *testing.T) {
	for _, test := range []struct {
		name  string
		input Result
		want  string
	}{
		{"allowed", Result{}, ""},
		{"policy finding", Result{ExitCode: 2, Stderr: "reconc blocked: protected path"}, "reconc policy would reject: protected path"},
		{"stop finding", Result{Stdout: `{"decision":"block","reason":"run required checks"}`}, "run required checks"},
		{"context", Result{Stdout: `{"additionalContext":"review the policy"}`}, "review the policy"},
		{"worker failure", Result{ExitCode: 2, Err: errors.New("worker unavailable")}, "worker unavailable"},
		{"malformed response", Result{Stdout: `{`}, "unreadable advisory result"},
	} {
		t.Run(test.name, func(t *testing.T) {
			result := AdaptDSHResult(test.input)
			if result.ExitCode != 0 || result.Stderr != "" || !errors.Is(result.Err, test.input.Err) {
				t.Fatalf("advisory result lost error identity or vetoed dispatch: %+v", result)
			}
			if test.want == "" {
				if result.Stdout != "" {
					t.Fatalf("allowed result created feedback: %q", result.Stdout)
				}
				return
			}
			var output struct {
				Advisory bool   `json:"advisory"`
				Reason   string `json:"reason"`
				Decision string `json:"decision"`
			}
			if err := json.Unmarshal([]byte(result.Stdout), &output); err != nil || !output.Advisory || output.Decision != "" || !strings.Contains(output.Reason, test.want) {
				t.Fatalf("advisory envelope = %s, error = %v", result.Stdout, err)
			}
		})
	}
}

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
				if parsed.MCP == nil || parsed.MCP.Platform != policy.MCPPlatform("custom:dsh") || parsed.MCP.Tool != test.native || parsed.MCP.BlockingPreHook {
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

func TestNormalizeDSHWorkingDirectoriesAndOpaqueTools(t *testing.T) {
	repo, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	nested := filepath.Join(repo, "nested")
	if err := os.Mkdir(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name, cwd, tool, input, extra, wantTool, wantPath string
	}{
		{"nested read", nested, "read", `{"file_path":"read.md"}`, "", "Read", "nested/read.md"},
		{"nested edit", nested, "edit", `{"file_path":"../root.md"}`, "", "Edit", "root.md"},
		{"nested editor", nested, "str_replace_editor", `{"command":"create","path":"out.go"}`, "", "Write", "nested/out.go"},
		{"future editor command", repo, "str_replace_editor", `{"command":"undo_edit","path":"out.go"}`, "", "str_replace_editor", "out.go"},
		{"PowerShell", repo, "pwsh", `{"command":"Write-Output test"}`, "", "pwsh", ""},
		{"PTC", repo, "run_code", `{"code":"await tools.read({file_path:'read.md'})"}`, "", "run_code", ""},
		{"terminal", repo, "terminal_send", `{"sessionId":"shell","text":"pwd"}`, "", "terminal_send", ""},
		{"persistent Bash", repo, "bash", `{"command":"pwd"}`, `,"bash_stateful":true`, "dsh:bash", ""},
		{"nested Bash", nested, "bash", `{"command":"pwd"}`, "", "dsh:bash", ""},
		{"Bash nested workdir", repo, "bash", `{"command":"pwd","workdir":"nested"}`, "", "dsh:bash", ""},
		{"Bash external workdir", repo, "bash", fmt.Sprintf(`{"command":"pwd","workdir":%q}`, filepath.Dir(repo)), "", "dsh:bash", ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			payload := fmt.Sprintf(`{"hook_event_name":"tools/pre-execute","session_id":"s","cwd":%q,"tool_name":%q,"tool_input":%s,"tool_call_id":"c","root_call_id":"r"%s}`, test.cwd, test.tool, test.input, test.extra)
			body, err := NormalizeDSHPayload("dsh-pre-tool-use", []byte(payload), repo)
			if err != nil {
				t.Fatal(err)
			}
			parsed, err := ParsePayload(body)
			if err != nil || parsed.ToolName != test.wantTool || parsed.FilePath() != test.wantPath {
				t.Fatalf("normalization = %+v, %v", parsed, err)
			}
			if parsed.MCP == nil || parsed.MCP.Tool != test.tool || parsed.MCP.BlockingPreHook {
				t.Fatalf("advisory selector = %+v", parsed.MCP)
			}
		})
	}
}

func TestNormalizeDSHRejectsUnboundOrMisleadingEvents(t *testing.T) {
	repo, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	valid := fmt.Sprintf(`{"hook_event_name":"tools/pre-execute","session_id":"s","cwd":%q,"tool_name":"str_replace_editor","tool_input":{"command":"create","path":"generated/out.go"},"tool_call_id":"c","root_call_id":"r"}`, repo)
	for _, test := range []struct {
		name, route, payload, want string
	}{
		{name: "empty", route: "dsh-pre-tool-use", payload: "", want: "empty DSH payload"},
		{name: "unknown route", route: "dsh-unknown", payload: valid, want: "unsupported DSH hook route"},
		{name: "event mismatch", route: "dsh-post-tool-use", payload: valid, want: "does not match route"},
		{name: "missing root call", route: "dsh-pre-tool-use", payload: strings.Replace(valid, `"root_call_id":"r"`, `"root_call_id":""`, 1), want: "requires name, call ID, and root call ID"},
		{name: "nonobject input", route: "dsh-pre-tool-use", payload: strings.Replace(valid, `"tool_input":{"command":"create","path":"generated/out.go"}`, `"tool_input":"create"`, 1), want: "JSON object"},
		{name: "post lacks observed marker", route: "dsh-post-tool-use", payload: strings.Replace(valid, `"tools/pre-execute"`, `"tools/result"`, 1), want: "final result observation"},
		{name: "outside cwd", route: "dsh-pre-tool-use", payload: strings.Replace(valid, repo, filepath.Dir(repo), 1), want: "outside repository root"},
		{name: "stop missing active", route: "dsh-stop", payload: fmt.Sprintf(`{"hook_event_name":"agent/turn-stopping","session_id":"s","cwd":%q}`, repo), want: "requires stop_hook_active"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := NormalizeDSHPayload(test.route, []byte(test.payload), repo); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("normalization error = %v, want %q", err, test.want)
			}
		})
	}
}
