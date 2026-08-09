package agentsession

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"reconc.dev/reconc/internal/policy"
)

func TestNormalizeOMPPayloadCoversEveryNativeRoute(t *testing.T) {
	repo := t.TempDir()
	for route, nativeEvent := range ompNativeEvents {
		t.Run(route, func(t *testing.T) {
			extra := ""
			switch route {
			case "omp-user-prompt-submit":
				extra = `,"prompt":"ship it","input_source":"interactive"`
			case "omp-pre-tool-use":
				extra = `,"tool_name":"write","tool_input":{"path":"docs/x.md"},"tool_call_id":"call-1"`
			case "omp-user-python":
				extra = fmt.Sprintf(`,"user_python_cwd":%q,"exclude_from_context":true,"code_bytes":42`, repo)
			case "omp-user-bash":
				extra = fmt.Sprintf(`,"tool_name":"bash","tool_input":{"command":"ls"},"user_bash_cwd":%q,"exclude_from_context":false`, repo)
			case "omp-permission-request":
				extra = `,"tool_name":"bash","tool_call_id":"call-1","approval_mode":"always-ask"`
			case "omp-permission-result":
				extra = `,"tool_name":"bash","tool_call_id":"call-1","approved":false`
			case "omp-post-tool-use":
				extra = `,"tool_name":"bash","tool_input":{"command":"true"},"tool_response":{"success":true,"exit_code":0},"tool_call_id":"call-1","is_error":false`
			case "omp-post-tool-use-failure":
				extra = `,"tool_name":"bash","tool_input":{"command":"false"},"tool_response":{"success":false,"exit_code":1},"tool_call_id":"call-1","is_error":true,"error":"failed"`
			case "omp-stop":
				extra = `,"stop_hook_active":false`
			}
			payload := fmt.Sprintf(`{"hook_event_name":%q,"session_id":"omp-s1","cwd":%q%s}`, nativeEvent, repo, extra)
			body, err := NormalizeOMPPayload(route, []byte(payload), repo)
			if err != nil {
				t.Fatalf("NormalizeOMPPayload: %v", err)
			}
			parsed, err := ParsePayload(body)
			if err != nil {
				t.Fatalf("ParsePayload: %v", err)
			}
			if parsed.SessionID != "omp-s1" || parsed.Raw["reconc_runtime"] != "omp" || parsed.Raw["omp_event"] != route {
				t.Fatalf("normalized identity = %#v", parsed.Raw)
			}
			if route == "omp-stop" && (!parsed.StrictContinuation || parsed.StopHookActive) {
				t.Fatalf("OMP Stop contract = %#v", parsed)
			}
		})
	}
}

func TestNormalizeOMPPayloadPreservesToolAndMCPIdentity(t *testing.T) {
	repo := t.TempDir()
	pre := fmt.Sprintf(`{
		"hook_event_name":"tool_call",
		"session_id":"omp-s1",
		"cwd":%q,
		"tool_name":"write",
		"tool_input":{"path":"docs/x.md"},
		"tool_call_id":"call-1"
	}`, repo)
	body, err := NormalizeOMPPayload("omp-pre-tool-use", []byte(pre), repo)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := ParsePayload(body)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.ToolName != "Write" || parsed.FilePath() != "docs/x.md" || parsed.ToolUseID != "call-1" {
		t.Fatalf("normalized OMP tool = %#v", parsed)
	}
	if parsed.MCP == nil || parsed.MCP.Platform != policy.MCPPlatformOMP || parsed.MCP.Tool != "write" || parsed.MCP.Observed || !parsed.MCP.BlockingPreHook || !parsed.MCP.InputValid || parsed.MCP.Outcome != "" {
		t.Fatalf("normalized OMP MCP envelope = %#v", parsed.MCP)
	}

	success := fmt.Sprintf(`{
		"hook_event_name":"tool_result",
		"session_id":"omp-s1",
		"cwd":%q,
		"tool_name":"bash",
		"tool_input":{"command":"go test ./..."},
		"tool_response":{"content":[],"details":{},"success":true,"exit_code":0},
		"tool_call_id":"call-2",
		"is_error":false
	}`, repo)
	body, err = NormalizeOMPPayload("omp-post-tool-use", []byte(success), repo)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err = ParsePayload(body)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.ToolName != "Bash" || parsed.Command() != "go test ./..." || parsed.ExitCode() == nil || *parsed.ExitCode() != 0 || parsed.MCP == nil || parsed.MCP.Outcome != "success" {
		t.Fatalf("normalized OMP success = %#v", parsed)
	}

	failure := strings.Replace(success, `"success":true,"exit_code":0`, `"success":false,"exit_code":1,"error":"failed"`, 1)
	failure = strings.Replace(failure, `"is_error":false`, `"is_error":true,"error":"failed"`, 1)
	body, err = NormalizeOMPPayload("omp-post-tool-use-failure", []byte(failure), repo)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err = ParsePayload(body)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.ExitCode() == nil || *parsed.ExitCode() != 1 || parsed.Error != "failed" || parsed.MCP == nil || parsed.MCP.Outcome != "failure" {
		t.Fatalf("normalized OMP failure = %#v", parsed)
	}
}

func TestNormalizeOMPPayloadRejectsUnsafeShapes(t *testing.T) {
	repo := t.TempDir()
	outside := t.TempDir()
	deep := strings.Repeat(`[`, MaxJSONDepth+1) + strings.Repeat(`]`, MaxJSONDepth+1)
	tests := []struct {
		name  string
		route string
		body  string
		want  string
	}{
		{name: "unknown route", route: "omp-unknown", body: `{}`, want: "unsupported OMP hook route"},
		{name: "empty", route: "omp-stop", body: ``, want: "empty OMP payload"},
		{name: "invalid JSON", route: "omp-stop", body: `{`, want: "unbalanced JSON braces"},
		{name: "depth", route: "omp-stop", body: deep, want: "levels of JSON nesting"},
		{name: "route mismatch", route: "omp-stop", body: fmt.Sprintf(`{"hook_event_name":"tool_call","session_id":"s1","cwd":%q}`, repo), want: "does not match route"},
		{name: "missing session", route: "omp-stop", body: fmt.Sprintf(`{"hook_event_name":"session_stop","cwd":%q,"stop_hook_active":false}`, repo), want: "missing non-empty session_id"},
		{name: "missing cwd", route: "omp-stop", body: `{"hook_event_name":"session_stop","session_id":"s1","stop_hook_active":false}`, want: "missing non-empty cwd"},
		{name: "cwd escape", route: "omp-stop", body: fmt.Sprintf(`{"hook_event_name":"session_stop","session_id":"s1","cwd":%q,"stop_hook_active":false}`, outside), want: "outside repository root"},
		{name: "missing tool name", route: "omp-pre-tool-use", body: fmt.Sprintf(`{"hook_event_name":"tool_call","session_id":"s1","cwd":%q,"tool_input":{}}`, repo), want: "missing tool_name"},
		{name: "missing tool call ID", route: "omp-pre-tool-use", body: fmt.Sprintf(`{"hook_event_name":"tool_call","session_id":"s1","cwd":%q,"tool_name":"write","tool_input":{}}`, repo), want: "missing tool_call_id"},
		{name: "missing tool input", route: "omp-pre-tool-use", body: fmt.Sprintf(`{"hook_event_name":"tool_call","session_id":"s1","cwd":%q,"tool_name":"write","tool_call_id":"call-1"}`, repo), want: "tool_input"},
		{name: "non-object tool input", route: "omp-pre-tool-use", body: fmt.Sprintf(`{"hook_event_name":"tool_call","session_id":"s1","cwd":%q,"tool_name":"write","tool_call_id":"call-1","tool_input":[]}`, repo), want: "must be a JSON object"},
		{name: "missing is error", route: "omp-post-tool-use", body: fmt.Sprintf(`{"hook_event_name":"tool_result","session_id":"s1","cwd":%q,"tool_name":"read","tool_call_id":"call-1","tool_input":{},"tool_response":{}}`, repo), want: "missing is_error"},
		{name: "wrong result route", route: "omp-post-tool-use", body: fmt.Sprintf(`{"hook_event_name":"tool_result","session_id":"s1","cwd":%q,"tool_name":"read","tool_call_id":"call-1","tool_input":{},"tool_response":{},"is_error":true}`, repo), want: "does not match route"},
		{name: "non-object tool response", route: "omp-post-tool-use", body: fmt.Sprintf(`{"hook_event_name":"tool_result","session_id":"s1","cwd":%q,"tool_name":"read","tool_call_id":"call-1","tool_input":{},"tool_response":[],"is_error":false}`, repo), want: "tool_response"},
		{name: "missing approval", route: "omp-permission-result", body: fmt.Sprintf(`{"hook_event_name":"tool_approval_resolved","session_id":"s1","cwd":%q,"tool_name":"bash","tool_call_id":"call-1"}`, repo), want: "missing approved decision"},
		{name: "missing stop active", route: "omp-stop", body: fmt.Sprintf(`{"hook_event_name":"session_stop","session_id":"s1","cwd":%q}`, repo), want: "missing stop_hook_active"},
		{name: "trailing value", route: "omp-stop", body: fmt.Sprintf(`{"hook_event_name":"session_stop","session_id":"s1","cwd":%q,"stop_hook_active":false} {}`, repo), want: "multiple JSON values"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := NormalizeOMPPayload(test.route, []byte(test.body), repo)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want substring %q", err, test.want)
			}
		})
	}
}

func TestNormalizeOMPPayloadAcceptsResolvedSubdirectory(t *testing.T) {
	repo := t.TempDir()
	child := filepath.Join(repo, "nested")
	if err := os.Mkdir(child, 0o755); err != nil {
		t.Fatal(err)
	}
	payload := fmt.Sprintf(`{"hook_event_name":"session_stop","session_id":"s1","cwd":%q,"stop_hook_active":true}`, child)
	body, err := NormalizeOMPPayload("omp-stop", []byte(payload), repo)
	if err != nil {
		t.Fatal(err)
	}
	var normalized map[string]interface{}
	if err := json.Unmarshal(body, &normalized); err != nil {
		t.Fatal(err)
	}
	if normalized["strict_continuation"] != true || normalized["stop_hook_active"] != true {
		t.Fatalf("normalized Stop = %#v", normalized)
	}
}

// TestOMPUserBashIsGatedLikeAToolCall covers the gap the host event records
// exposed: Oh My Pi publishes `user_bash` with the same full-replacement result
// contract as a tool call, so a command the user types must reach the same
// policy decision instead of running unobserved.
func TestOMPUserBashIsGatedLikeAToolCall(t *testing.T) {
	repo := t.TempDir()
	valid := fmt.Sprintf(`{"hook_event_name":"user_bash","session_id":"omp-s1","cwd":%q,`+
		`"tool_name":"bash","tool_input":{"command":"rm -rf build"},"user_bash_cwd":%q,"exclude_from_context":true}`, repo, repo)
	body, err := NormalizeOMPPayload("omp-user-bash", []byte(valid), repo)
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}
	parsed, err := ParsePayload(body)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if parsed.ToolName != "Bash" {
		t.Fatalf("tool identity = %q, want the normalized Bash identity", parsed.ToolName)
	}
	if parsed.ToolInput["command"] != "rm -rf build" {
		t.Fatalf("command = %v, want the exact command the user typed", parsed.ToolInput["command"])
	}
	if parsed.MCP == nil {
		t.Fatal("the pre-execution route must carry the exact identity envelope")
	}

	for name, payload := range map[string]string{
		"foreign tool identity": fmt.Sprintf(`{"hook_event_name":"user_bash","session_id":"s","cwd":%q,`+
			`"tool_name":"write","tool_input":{"command":"ls"},"user_bash_cwd":%q,"exclude_from_context":false}`, repo, repo),
		"missing context flag": fmt.Sprintf(`{"hook_event_name":"user_bash","session_id":"s","cwd":%q,`+
			`"tool_name":"bash","tool_input":{"command":"ls"},"user_bash_cwd":%q}`, repo, repo),
		"working directory outside the repository": fmt.Sprintf(`{"hook_event_name":"user_bash","session_id":"s","cwd":%q,`+
			`"tool_name":"bash","tool_input":{"command":"ls"},"user_bash_cwd":"/","exclude_from_context":false}`, repo),
		"command is not an object": fmt.Sprintf(`{"hook_event_name":"user_bash","session_id":"s","cwd":%q,`+
			`"tool_name":"bash","tool_input":"ls","user_bash_cwd":%q,"exclude_from_context":false}`, repo, repo),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := NormalizeOMPPayload("omp-user-bash", []byte(payload), repo); err == nil {
				t.Fatal("a malformed user_bash payload must be refused before it can decide anything")
			}
		})
	}
}

// TestOMPUserPythonIsObservedWithoutItsSource covers the surface next to the
// user_bash gate. Python cannot be decided against a policy that reads shell
// grammar, but it can start a shell, so leaving it invisible would make that
// gate look wider than it is. The code itself never leaves the host.
func TestOMPUserPythonIsObservedWithoutItsSource(t *testing.T) {
	repo := t.TempDir()
	payload := fmt.Sprintf(`{"hook_event_name":"user_python","session_id":"omp-s1","cwd":%q,`+
		`"user_python_cwd":%q,"exclude_from_context":false,"code_bytes":128}`, repo, repo)
	body, err := NormalizeOMPPayload("omp-user-python", []byte(payload), repo)
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}
	if strings.Contains(string(body), "import ") || strings.Contains(string(body), "code\"") {
		t.Fatalf("normalized Python observation must not carry source: %s", body)
	}
	parsed, err := ParsePayload(body)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if parsed.SessionID != "omp-s1" {
		t.Fatalf("session identity = %q", parsed.SessionID)
	}
	if parsed.Raw["user_python_cwd"] != repo || parsed.Raw["exclude_from_context"] != false || parsed.Raw["code_bytes"] != json.Number("128") {
		t.Fatalf("redacted Python metadata was not preserved: %#v", parsed.Raw)
	}
	if parsed.ToolName != "" || parsed.MCP != nil {
		t.Fatal("an observation must not claim a tool identity or an MCP envelope")
	}

	for name, broken := range map[string]string{
		"missing context flag": fmt.Sprintf(`{"hook_event_name":"user_python","session_id":"s","cwd":%q,`+
			`"user_python_cwd":%q,"code_bytes":1}`, repo, repo),
		"missing code size": fmt.Sprintf(`{"hook_event_name":"user_python","session_id":"s","cwd":%q,`+
			`"user_python_cwd":%q,"exclude_from_context":false}`, repo, repo),
		"negative code size": fmt.Sprintf(`{"hook_event_name":"user_python","session_id":"s","cwd":%q,`+
			`"user_python_cwd":%q,"exclude_from_context":false,"code_bytes":-1}`, repo, repo),
		"working directory outside the repository": fmt.Sprintf(`{"hook_event_name":"user_python","session_id":"s","cwd":%q,`+
			`"user_python_cwd":"/","exclude_from_context":false,"code_bytes":1}`, repo),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := NormalizeOMPPayload("omp-user-python", []byte(broken), repo); err == nil {
				t.Fatal("a malformed observation must be refused rather than recorded as liveness")
			}
		})
	}
}

func TestOMPUserPythonObservationPersistsWithoutSource(t *testing.T) {
	t.Setenv(StateRootEnv, t.TempDir())
	repo := t.TempDir()
	root, err := ResolveRepoRoot(repo)
	if err != nil {
		t.Fatal(err)
	}
	normalize := func(codeBytes int, excluded bool) []byte {
		t.Helper()
		payload := fmt.Sprintf(`{"hook_event_name":"user_python","session_id":"omp-observation","cwd":%q,`+
			`"user_python_cwd":%q,"exclude_from_context":%t,"code_bytes":%d}`, repo, repo, excluded, codeBytes)
		body, err := NormalizeOMPPayload("omp-user-python", []byte(payload), repo)
		if err != nil {
			t.Fatalf("normalize: %v", err)
		}
		return body
	}
	for _, body := range [][]byte{normalize(128, false), normalize(256, true)} {
		parsed, err := ParsePayload(body)
		if err != nil {
			t.Fatal(err)
		}
		if parsed.Raw["reconc_runtime"] != "omp" || parsed.Raw["omp_event"] != "omp-user-python" {
			t.Fatalf("normalized observation identity = %#v", parsed.Raw)
		}
		result := runPassiveEventResolved(root, body)
		if result.ExitCode != 0 || result.Stderr != "" {
			t.Fatalf("persist observation: %+v", result)
		}
	}
	records, err := ReadHookLiveness(repo)
	if err != nil {
		t.Fatal(err)
	}
	observation := records["omp"].Observations["omp-user-python"]
	if observation.Count != 2 || observation.WorkingDirectory != "." || observation.CodeBytes != 256 || !observation.ExcludeFromContext || observation.LastSeen == "" {
		t.Fatalf("persisted Python observation = %+v; records=%+v", observation, records)
	}
	encoded, err := json.Marshal(records)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "import ") || strings.Contains(string(encoded), `"code":`) {
		t.Fatalf("persisted observation leaked Python source: %s", encoded)
	}
}
