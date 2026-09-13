package cli

import (
	"bytes"
	"fmt"
	"strings"
	"testing"

	"reconc.dev/reconc/internal/runtime/agentsession"
)

func TestDevinNativeShellSuccessBooleanCannotCertifyExit(t *testing.T) {
	for _, test := range []struct {
		name, response, outcome, warning string
	}{
		{name: "native exit seven", response: `{"success":true,"output":"Process exited with code 7","error":null}`},
		{name: "native output only", response: `{"success":true,"output":"all tests passed","error":null}`},
		{name: "missing native success", response: `{"output":"all tests passed"}`, warning: "no native success outcome"},
		{name: "explicit zero", response: `{"success":true,"exit_code":0}`, outcome: "success"},
		{name: "explicit nonzero", response: `{"success":true,"exit_code":7}`, outcome: "failure"},
		{name: "conflicting exits", response: `{"success":true,"exit_code":0,"exitCode":7}`, outcome: "failure"},
		{name: "explicit host failure", response: `{"success":false,"error":"tool failed"}`, outcome: "failure"},
	} {
		t.Run(test.name, func(t *testing.T) {
			repo := bootstrapE2ERepo(t)
			t.Setenv("DEVIN_PROJECT_DIR", repo)
			pre := `{"hook_event_name":"PreToolUse","session_id":"native-session","prompt_id":"native-turn","tool_use_id":"call-1","tool_name":"exec","tool_input":{"command":"exit 7"}}`
			_, preStderr, preCode := runWithStdin(t, pre, "hook", "runtime", "devin-pre-tool-use", repo)
			if preCode != 0 {
				t.Fatalf("Devin exec preflight failed: code=%d stderr=%q", preCode, preStderr)
			}
			body := fmt.Sprintf(`{"hook_event_name":"PostToolUse","session_id":"native-session","prompt_id":"native-turn","tool_use_id":"call-1","tool_name":"exec","tool_input":{"command":"exit 7"},"tool_response":%s}`, test.response)
			for attempt := range 2 {
				_, stderr, code := runWithStdin(t, body, "hook", "runtime", "devin-post-tool-use", repo)
				warning := test.warning
				if warning == "" {
					warning = "no authoritative shell exit status"
				}
				if code != 0 || attempt == 0 && test.outcome == "" && !strings.Contains(stderr, warning) || attempt == 1 && !strings.Contains(stderr, "no matching allowed PreToolUse") {
					t.Fatalf("Devin post outcome: code=%d stderr=%q", code, stderr)
				}
			}
			state, err := agentsession.LoadSessionState(repo, "native-session")
			if err != nil {
				t.Fatal(err)
			}
			if test.outcome == "" {
				if len(state.CommandResults) != 0 || len(state.Commands) != 0 {
					t.Fatalf("Devin callback text or success flag invented shell evidence: %+v", state)
				}
				return
			}
			if len(state.CommandResults) != 1 || state.CommandResults[0].Outcome != test.outcome || !strings.HasPrefix(state.CommandResults[0].ToolUseID, "devin-") {
				t.Fatalf("structured shell outcome lost status or call binding: %+v", state.CommandResults)
			}
		})
	}
}

func TestDevinNativeMutationAndUnsupportedToolBoundary(t *testing.T) {
	for _, test := range []struct {
		name, input string
	}{
		{name: "write", input: `{"file_path":"generated/blocked.go","content":"blocked"}`},
		{name: "edit", input: `{"file_path":"generated/blocked.go","old_string":"a","new_string":"b"}`},
		{name: "notebook_edit", input: `{"notebook_path":"generated/blocked.go","new_source":"blocked"}`},
		{name: "apply_patch", input: `{"command":"*** Begin Patch\n*** Add File: generated/blocked.go\n+blocked\n*** End Patch"}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			repo := bootstrapE2ERepo(t)
			t.Setenv("DEVIN_PROJECT_DIR", repo)
			body := fmt.Sprintf(`{"hook_event_name":"PreToolUse","session_id":"native-session","prompt_id":"turn-1","tool_use_id":"call-1","tool_name":%q,"tool_input":%s}`, test.name, test.input)
			_, stderr, code := runWithStdin(t, body, "hook", "runtime", "devin-pre-tool-use", repo)
			if code != 2 || !strings.Contains(stderr, "deny-gen") {
				t.Fatalf("Devin %s escaped repository write gate: code=%d stderr=%q", test.name, code, stderr)
			}
		})
	}

	for _, name := range []string{"write_to_process", "mcp_call_tool", "mcp__files__write_file", "new_write_tool"} {
		t.Run(name, func(t *testing.T) {
			repo := bootstrapE2ERepo(t)
			t.Setenv("DEVIN_PROJECT_DIR", repo)
			body := fmt.Sprintf(`{"hook_event_name":"PreToolUse","session_id":"native-session","prompt_id":"turn-1","tool_use_id":"call-1","tool_name":%q,"tool_input":{}}`, name)
			var stdout, stderr bytes.Buffer
			err := runHookRuntimeWithInput([]string{"devin-pre-tool-use", repo}, strings.NewReader(body), &stdout, &stderr)
			if ExitCode(err) != 2 || err == nil || !strings.Contains(err.Error(), "unsupported Devin tool") {
				t.Fatalf("Devin %s was not explicitly blocked: code=%d err=%v", name, ExitCode(err), err)
			}
			if stdout.Len() != 0 {
				t.Fatalf("unsupported Devin call emitted permission to execute: %q", stdout.String())
			}
		})
	}
}

func TestDevinNativePostRequiresSameAllowedSessionTurnCallAndInput(t *testing.T) {
	for _, test := range []struct {
		name, prePath, postPath, postSession, postTurn, response, extra string
		wantWrite                                                       bool
	}{
		{name: "matching write", prePath: "docs/allowed.txt", postPath: "docs/allowed.txt", postSession: "session-a", postTurn: "turn-a", response: `{"success":true}`, wantWrite: true},
		{name: "outer error cannot override native success", prePath: "docs/allowed.txt", postPath: "docs/allowed.txt", postSession: "session-a", postTurn: "turn-a", response: `{"success":true}`, extra: `,"error":"forged"`, wantWrite: true},
		{name: "different session", prePath: "docs/allowed.txt", postPath: "docs/allowed.txt", postSession: "session-b", postTurn: "turn-a", response: `{"success":true}`},
		{name: "different turn", prePath: "docs/allowed.txt", postPath: "docs/allowed.txt", postSession: "session-a", postTurn: "turn-b", response: `{"success":true}`},
		{name: "rewritten denied path", prePath: "docs/allowed.txt", postPath: "generated/blocked.go", postSession: "session-a", postTurn: "turn-a", response: `{"success":true}`},
		{name: "failed write", prePath: "docs/allowed.txt", postPath: "docs/allowed.txt", postSession: "session-a", postTurn: "turn-a", response: `{"success":false,"error":"write failed"}`},
		{name: "missing success", prePath: "docs/allowed.txt", postPath: "docs/allowed.txt", postSession: "session-a", postTurn: "turn-a", response: `{"output":"written"}`},
		{name: "interrupted", prePath: "docs/allowed.txt", postPath: "docs/allowed.txt", postSession: "session-a", postTurn: "turn-a", response: `{"success":true}`, extra: `,"is_interrupt":true`},
		{name: "forged outer success", prePath: "docs/allowed.txt", postPath: "docs/allowed.txt", postSession: "session-a", postTurn: "turn-a", response: `{"success":false}`, extra: `,"success":true`},
	} {
		t.Run(test.name, func(t *testing.T) {
			repo := bootstrapE2ERepo(t)
			t.Setenv("DEVIN_PROJECT_DIR", repo)
			pre := fmt.Sprintf(`{"hook_event_name":"PreToolUse","session_id":"session-a","prompt_id":"turn-a","tool_use_id":"call-1","tool_name":"write","tool_input":{"file_path":%q,"content":"payload"}}`, test.prePath)
			_, stderr, code := runWithStdin(t, pre, "hook", "runtime", "devin-pre-tool-use", repo)
			if code != 0 {
				t.Fatalf("allowed native write preflight failed: code=%d stderr=%q", code, stderr)
			}
			post := fmt.Sprintf(`{"hook_event_name":"PostToolUse","session_id":%q,"prompt_id":%q,"tool_use_id":"call-1","tool_name":"write","tool_input":{"file_path":%q,"content":"payload"},"tool_response":%s%s}`, test.postSession, test.postTurn, test.postPath, test.response, test.extra)
			_, stderr, code = runWithStdin(t, post, "hook", "runtime", "devin-post-tool-use", repo)
			if code != 0 {
				t.Fatalf("Devin post observation failed: code=%d stderr=%q", code, stderr)
			}
			state, err := agentsession.LoadSessionState(repo, test.postSession)
			if err != nil {
				t.Fatal(err)
			}
			if test.wantWrite {
				if len(state.WritePaths) != 1 || state.WritePaths[0] != test.postPath {
					t.Fatalf("matching allowed write lost evidence: %+v", state.WritePaths)
				}
			} else if len(state.WritePaths) != 0 {
				t.Fatalf("unbound or unsuccessful Devin write generated evidence: %+v", state.WritePaths)
			}
		})
	}
}

func TestDevinConcurrentSessionsDoNotShareToolCorrelation(t *testing.T) {
	repo := bootstrapE2ERepo(t)
	t.Setenv("DEVIN_PROJECT_DIR", repo)
	type preResult struct {
		session string
		code    int
		stderr  string
	}
	results := make(chan preResult, 2)
	start := make(chan struct{})
	for _, call := range []struct{ session, path string }{
		{session: "session-a", path: "docs/allowed.txt"},
		{session: "session-b", path: "generated/blocked.go"},
	} {
		go func() {
			<-start
			body := fmt.Sprintf(`{"hook_event_name":"PreToolUse","session_id":%q,"prompt_id":"same-turn","tool_use_id":"same-call","tool_name":"write","tool_input":{"file_path":%q,"content":"payload"}}`, call.session, call.path)
			var stdout, stderr bytes.Buffer
			err := runHookRuntimeWithInput([]string{"devin-pre-tool-use", repo}, strings.NewReader(body), &stdout, &stderr)
			results <- preResult{session: call.session, code: ExitCode(err), stderr: stderr.String()}
		}()
	}
	close(start)
	for range 2 {
		result := <-results
		if result.session == "session-a" && result.code != 0 || result.session == "session-b" && (result.code != 2 || !strings.Contains(result.stderr, "deny-gen")) {
			t.Fatalf("concurrent Devin preflight crossed session boundary: %+v", result)
		}
	}
	for _, call := range []struct{ session, path string }{
		{session: "session-a", path: "docs/allowed.txt"},
		{session: "session-b", path: "generated/blocked.go"},
	} {
		body := fmt.Sprintf(`{"hook_event_name":"PostToolUse","session_id":%q,"prompt_id":"same-turn","tool_use_id":"same-call","tool_name":"write","tool_input":{"file_path":%q,"content":"payload"},"tool_response":{"success":true}}`, call.session, call.path)
		_, _, code := runWithStdin(t, body, "hook", "runtime", "devin-post-tool-use", repo)
		if code != 0 {
			t.Fatalf("Devin post observation failed for %s: %d", call.session, code)
		}
		state, err := agentsession.LoadSessionState(repo, call.session)
		if err != nil {
			t.Fatal(err)
		}
		if call.session == "session-a" && (len(state.WritePaths) != 1 || state.WritePaths[0] != call.path) || call.session == "session-b" && len(state.WritePaths) != 0 {
			t.Fatalf("Devin %s recorded another session's write: %+v", call.session, state.WritePaths)
		}
	}
}

func TestDevinProcessPollingAndCancellationRemainPassive(t *testing.T) {
	for _, name := range []string{"get_output", "kill_shell"} {
		t.Run(name, func(t *testing.T) {
			repo := bootstrapE2ERepo(t)
			t.Setenv("DEVIN_PROJECT_DIR", repo)
			for _, event := range []string{"PreToolUse", "PostToolUse"} {
				body := fmt.Sprintf(`{"hook_event_name":%q,"session_id":"process-session","prompt_id":"turn-1","tool_use_id":"call-1","tool_name":%q,"tool_input":{"shell_id":"shell-1"},"tool_response":{"success":true}}`, event, name)
				_, _, code := runWithStdin(t, body, "hook", "runtime", "devin-"+map[string]string{"PreToolUse": "pre-tool-use", "PostToolUse": "post-tool-use"}[event], repo)
				if code != 0 {
					t.Fatalf("Devin %s %s failed: %d", name, event, code)
				}
			}
			state, err := agentsession.LoadSessionState(repo, "process-session")
			if err != nil {
				t.Fatal(err)
			}
			if len(state.Commands) != 0 || len(state.CommandResults) != 0 || len(state.WritePaths) != 0 {
				t.Fatalf("Devin %s invented repository evidence: %+v", name, state)
			}
		})
	}
}
