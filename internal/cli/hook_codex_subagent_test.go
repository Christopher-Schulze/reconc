package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"reconc.dev/reconc/internal/compiler"
	"reconc.dev/reconc/internal/runtime/agentsession"
)

func TestCodexSubagentStopRetainsChildEvidenceAndDoesNotOwnParentRun(t *testing.T) {
	for _, activeTask := range []bool{false, true} {
		name := "without-executable-task"
		if activeTask {
			name = "with-executable-task"
		}
		t.Run(name, func(t *testing.T) {
			repo := bootstrapE2ERepo(t)
			if activeTask {
				writeHookRuntimeTaskFixture(t, repo)
			}
			for _, input := range []struct{ route, body string }{
				{"codex-session-start", `{"session_id":"parent"}`},
				{"codex-post-tool-use", `{"session_id":"parent","tool_name":"Read","tool_input":{"file_path":"README.md"}}`},
				{"codex-subagent-start", `{"session_id":"parent","agent_id":"child","hook_event_name":"SubagentStart"}`},
				{"codex-post-tool-use", `{"session_id":"parent","agent_id":"child","tool_use_id":"child-write","tool_name":"Write","tool_input":{"file_path":"src/app.go"}}`},
			} {
				if _, stderr, code := runWithStdin(t, input.body, "hook", "runtime", input.route, repo); code != 0 || stderr != "" {
					t.Fatalf("setup %s: code=%d stderr=%s", input.route, code, stderr)
				}
			}
			if _, err := agentsession.SetRepositoryRun(repo, true); err != nil {
				t.Fatal(err)
			}
			parentBefore, err := agentsession.LoadSessionState(repo, "parent")
			if err != nil {
				t.Fatal(err)
			}
			runBefore, err := agentsession.ReadRepositoryRunStatus(repo)
			if err != nil {
				t.Fatal(err)
			}
			for _, step := range []struct {
				name, body string
				blocked    bool
			}{
				{"first-turn", `{"session_id":"parent","agent_id":"child","hook_event_name":"SubagentStop","turn_id":"one"}`, true},
				{"remediation-bound", `{"session_id":"parent","agent_id":"child","hook_event_name":"SubagentStop","turn_id":"one","stop_hook_active":true,"strict_continuation":true}`, false},
				{"next-turn-still-checks", `{"session_id":"parent","agent_id":"child","hook_event_name":"SubagentStop","turn_id":"two"}`, true},
			} {
				stdout, stderr, code := runWithStdin(t, step.body, "hook", "runtime", "codex-subagent-stop", repo)
				if code != 0 {
					t.Fatalf("%s: code=%d stderr=%s", step.name, code, stderr)
				}
				if step.blocked {
					var decision struct {
						Decision string `json:"decision"`
						Reason   string `json:"reason"`
					}
					decoder := json.NewDecoder(strings.NewReader(stdout))
					decoder.DisallowUnknownFields()
					if err := decoder.Decode(&decision); err != nil || decision.Decision != "block" || !strings.Contains(decision.Reason, "need-ci") {
						t.Fatalf("%s did not emit a native policy block: %s error=%v", step.name, stdout, err)
					}
				} else {
					state, err := agentsession.LoadSessionState(repo, "child")
					if stdout != "" || !strings.Contains(stderr, "uncertified") || err != nil || !state.UncertifiedTermination {
						t.Fatalf("recursive stop not released as uncertified: stdout=%s stderr=%s state=%+v error=%v", stdout, stderr, state, err)
					}
				}
			}
			if stdout, stderr, code := runWithStdin(t, "", "hook", "claim", repo, "ci-green"); code != 0 {
				t.Fatalf("repair actual policy claim: code=%d stdout=%s stderr=%s", code, stdout, stderr)
			}
			stdout, stderr, code := runWithStdin(t, `{"session_id":"parent","agent_id":"child","hook_event_name":"SubagentStop","turn_id":"three"}`, "hook", "runtime", "codex-subagent-stop", repo)
			if code != 0 || stdout != "" || stderr != "" {
				t.Fatalf("clean child inherited parent continuation: code=%d stdout=%s stderr=%s", code, stdout, stderr)
			}
			child, err := agentsession.LoadSessionState(repo, "child")
			if err != nil || child.SessionID != "child" || len(child.WritePaths) != 1 || child.WritePaths[0] != "src/app.go" || child.UncertifiedTermination || child.LastStopBlockViolationHash != "" || child.RepositoryRunAwaiting {
				t.Fatalf("child evidence or completion state invalid: %+v error=%v", child, err)
			}
			parentAfter, err := agentsession.LoadSessionState(repo, "parent")
			if err != nil || !reflect.DeepEqual(parentBefore, parentAfter) {
				t.Fatalf("child modified parent evidence: before=%+v after=%+v error=%v", parentBefore, parentAfter, err)
			}
			runAfter, err := agentsession.ReadRepositoryRunStatus(repo)
			if err != nil || !runAfter.Enabled || !reflect.DeepEqual(runBefore, runAfter) {
				t.Fatalf("child changed durable run mode: before=%+v after=%+v error=%v", runBefore, runAfter, err)
			}
		})
	}
}

func TestCodexSubagentStopRejectsAmbiguousOrForeignIdentity(t *testing.T) {
	repo := bootstrapE2ERepo(t)
	for _, body := range []string{
		`{not-json`,
		`{}`,
		`{"session_id":" padded "}`,
		`{"session_id":"child","session_id":"parent"}`,
		`{"session_id":"parent","hook_event_name":"SubagentStop"}`,
		`{"session_id":"parent","agent_id":"","hook_event_name":"SubagentStop"}`,
		`{"session_id":"child","hook_event_name":"Stop"}`,
		`{"session_id":"child","agent_id":42}`,
	} {
		var stdout, stderr bytes.Buffer
		err := runHookRuntimeWithInput([]string{"codex-subagent-stop", repo}, strings.NewReader(body), &stdout, &stderr)
		// Runtime boundary failures return a CLIError that main renders; handler
		// failures write stderr directly. Check both real diagnostic channels.
		if ExitCode(err) != 2 || stdout.Len() != 0 || (stderr.Len() == 0 && (err == nil || err.Error() == "")) {
			t.Fatalf("invalid child stop did not fail closed: body=%s error=%v stdout=%s stderr=%s", body, err, stdout.String(), stderr.String())
		}
	}
}

func TestCodexNativeChildMCPAndLocalEvidenceRemainSeparate(t *testing.T) {
	repo := bootstrapE2ERepo(t)
	config := "mcp:\n  unclassified: deny\n  tools:\n    - platform: codex\n      tool: mcp__files__write\n      effect: repository_write\n      path_fields: [/path]\n"
	if err := os.WriteFile(filepath.Join(repo, ".reconc.yml"), []byte(config), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := compiler.CompileRepoPolicy(repo, "e2e"); err != nil {
		t.Fatal(err)
	}
	if _, stderr, code := runWithStdin(t, `{"session_id":"parent","tool_name":"Read","tool_input":{"file_path":"README.md"}}`, "hook", "runtime", "codex-post-tool-use", repo); code != 0 || stderr != "" {
		t.Fatalf("parent setup: code=%d stderr=%s", code, stderr)
	}
	parentBefore, err := agentsession.LoadSessionState(repo, "parent")
	if err != nil {
		t.Fatal(err)
	}
	for _, child := range []string{"child-a", "child-b"} {
		for _, input := range []struct{ route, fields string }{
			{"codex-subagent-start", `"hook_event_name":"SubagentStart"`},
			{"codex-pre-tool-use", `"tool_name":"Read","tool_input":{"file_path":"docs/guide.md"}`},
			{"codex-post-tool-use", `"tool_name":"Read","tool_use_id":"read","tool_input":{"file_path":"docs/guide.md"}`},
			{"codex-permission-request", `"tool_name":"mcp__files__write","tool_input":{"path":"docs/out.md"}`},
			{"codex-mcp-before", `"tool_name":"mcp__files__write","tool_use_id":"write","tool_input":{"path":"docs/out.md"}`},
			{"codex-mcp-after", `"tool_name":"mcp__files__write","tool_use_id":"write","tool_input":{"path":"docs/out.md"},"tool_response":{"isError":false}`},
		} {
			body := fmt.Sprintf(`{"session_id":"parent","agent_id":%q,%s}`, child, input.fields)
			if _, stderr, code := runWithStdin(t, body, "hook", "runtime", input.route, repo); code != 0 || stderr != "" {
				t.Fatalf("child %s route %s: code=%d stderr=%s", child, input.route, code, stderr)
			}
		}
		state, err := agentsession.LoadSessionState(repo, child)
		if err != nil || state.SessionID != child || !reflect.DeepEqual(state.ReadPaths, []string{"docs/guide.md"}) || !reflect.DeepEqual(state.WritePaths, []string{"docs/out.md"}) || state.MaterialEvents != 1 {
			t.Fatalf("native child evidence lost or merged: child=%s state=%+v error=%v", child, state, err)
		}
	}
	parentAfter, err := agentsession.LoadSessionState(repo, "parent")
	if err != nil || !reflect.DeepEqual(parentBefore, parentAfter) {
		t.Fatalf("native child metadata was dropped before MCP or local routing: before=%+v after=%+v error=%v", parentBefore, parentAfter, err)
	}
}

func TestCodexSubagentStopCompatibilityAndInterruptRemainBounded(t *testing.T) {
	for _, input := range []struct {
		name, body  string
		uncertified bool
	}{
		{"legacy-metadata-omitted", `{"session_id":"child"}`, false},
		{"explicit-interrupt", `{"session_id":"child","agent_id":"child","is_interrupt":true}`, true},
	} {
		t.Run(input.name, func(t *testing.T) {
			repo := bootstrapE2ERepo(t)
			if _, err := agentsession.SetRepositoryRun(repo, true); err != nil {
				t.Fatal(err)
			}
			stdout, stderr, code := runWithStdin(t, input.body, "hook", "runtime", "codex-subagent-stop", repo)
			state, err := agentsession.LoadSessionState(repo, "child")
			if code != 0 || stdout != "" || err != nil || state.UncertifiedTermination != input.uncertified {
				t.Fatalf("child turn outcome: code=%d stdout=%s stderr=%s state=%+v error=%v", code, stdout, stderr, state, err)
			}
			run, err := agentsession.ReadRepositoryRunStatus(repo)
			if err != nil || !run.Enabled {
				t.Fatalf("child disabled parent run: %+v error=%v", run, err)
			}
		})
	}
}

func TestCodexSubagentStopOverflowIsUncertifiedAndRetainsEvidence(t *testing.T) {
	repo := bootstrapE2ERepo(t)
	if _, stderr, code := runWithStdin(t, `{"session_id":"parent","agent_id":"child","tool_name":"Read","tool_input":{"file_path":"README.md"}}`, "hook", "runtime", "codex-post-tool-use", repo); code != 0 || stderr != "" {
		t.Fatalf("child setup: code=%d stderr=%s", code, stderr)
	}
	state, err := agentsession.MutateSessionState(repo, "child", func(current agentsession.SessionState) agentsession.SessionState {
		return agentsession.AppendCommand(current, strings.Repeat("x", agentsession.MaxSessionStateBytes+1))
	})
	if err != nil || !state.EvidenceOverflow || state.EvidenceOverflowReason != "commands" || state.EvidenceOverflowLimit != "item_bytes" {
		t.Fatalf("real command overflow was not recorded: state=%+v error=%v", state, err)
	}
	// Overflow rotates prior evidence into an immutable segment. The package
	// regression loads and verifies that chain; this CLI control preserves its head.
	segments, digest := state.EvidenceSegmentCount, state.EvidenceSegmentDigest
	if segments != 1 || digest == "" {
		t.Fatalf("overflow did not retain prior evidence in a segment: %+v", state)
	}
	if _, err := agentsession.SetRepositoryRun(repo, true); err != nil {
		t.Fatal(err)
	}
	stdout, stderr, code := runWithStdin(t, `{"session_id":"parent","agent_id":"child","hook_event_name":"SubagentStop"}`, "hook", "runtime", "codex-subagent-stop", repo)
	state, err = agentsession.LoadSessionState(repo, "child")
	if code != 0 || stdout != "" || !strings.Contains(stderr, "uncertified") || err != nil || !state.UncertifiedTermination || !state.EvidenceOverflow || state.EvidenceSegmentCount != segments || state.EvidenceSegmentDigest != digest {
		t.Fatalf("overflow was certified or evidence lost: code=%d stdout=%s stderr=%s state=%+v error=%v", code, stdout, stderr, state, err)
	}
	run, err := agentsession.ReadRepositoryRunStatus(repo)
	if err != nil || !run.Enabled {
		t.Fatalf("overflow disabled durable run: %+v error=%v", run, err)
	}
}

func TestCodexSubagentStopCannotCertifyCorruptPolicy(t *testing.T) {
	repo := bootstrapE2ERepo(t)
	if err := os.WriteFile(filepath.Join(repo, ".reconc", "policy.lock.json"), []byte(`{"corrupt":`), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, active := range []bool{false, true} {
		body := fmt.Sprintf(`{"session_id":"parent","agent_id":"child","hook_event_name":"SubagentStop","stop_hook_active":%t}`, active)
		stdout, stderr, code := runWithStdin(t, body, "hook", "runtime", "codex-subagent-stop", repo)
		if code != 2 || stdout != "" || !strings.Contains(stderr, "policy check failed") {
			t.Fatalf("corrupt policy was released or certified: code=%d stdout=%s stderr=%s", code, stdout, stderr)
		}
	}
}
