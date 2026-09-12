package cli

import (
	"bytes"
	"os"
	"strings"
	"testing"
	"time"
)

func TestCodexNativeStreamMatchesCapturedHostCommand(t *testing.T) {
	body, err := os.ReadFile("testdata/codex-native-command-stream.txt")
	if err != nil {
		t.Fatal(err)
	}
	stream, err := parseCodexNativeStream(body)
	if err != nil || len(stream.Commands) != 1 {
		t.Fatalf("stream=%+v error=%v", stream, err)
	}
	command := stream.Commands[0]
	if stream.SessionSHA256 != liveHookDigest([]byte("01a0973b-d55d-72e1-904e-1da86df3b826")) || command.ID != "item_3" || command.Command != "/opt/homebrew/bin/bash -lc 'printf PROBE_OK > allowed-command-marker'" || command.ExitCode != 0 {
		t.Fatalf("native identity or command changed: %+v", stream)
	}
}

func TestCodexNativeFileChangesBindStartedAndCompletedEvents(t *testing.T) {
	const prefix = "{\"type\":\"thread.started\",\"thread_id\":\"session\"}\n{\"type\":\"turn.started\"}\n"
	const start = "{\"type\":\"item.started\",\"item\":{\"id\":\"patch\",\"type\":\"file_change\",\"changes\":[{\"path\":\"marker\",\"kind\":\"add\"}],\"status\":\"in_progress\"}}\n"
	const finish = "{\"type\":\"item.completed\",\"item\":{\"id\":\"patch\",\"type\":\"file_change\",\"changes\":[{\"path\":\"marker\",\"kind\":\"add\"}],\"status\":\"completed\"}}\n"
	const suffix = "{\"type\":\"turn.completed\"}\n"
	for _, test := range []struct {
		name, events string
		valid        bool
	}{
		{"native-start-and-completion", start + finish, true},
		{"documented-completion-only", finish, true},
		{"missing-completion", start, false},
		{"duplicate-start", start + start + finish, false},
		{"duplicate-completion", start + finish + finish, false},
		{"changed-path", start + strings.Replace(finish, "marker", "different", 1), false},
		{"changed-kind", start + strings.Replace(finish, `"add"`, `"delete"`, 1), false},
		{"invalid-status", start + strings.Replace(finish, `"completed"`, `"in_progress"`, 1), false},
	} {
		t.Run(test.name, func(t *testing.T) {
			stream, err := parseCodexNativeStream([]byte(prefix + test.events + suffix))
			if (err == nil) != test.valid {
				t.Fatalf("stream=%+v error=%v", stream, err)
			}
			if test.valid && (len(stream.FileChanges) != 1 || stream.FileChanges[0].Path != "marker" || stream.FileChanges[0].Kind != "add" || stream.FileChanges[0].Status != "completed") {
				t.Fatalf("lost native file outcome: %+v", stream)
			}
		})
	}
}

func TestCodexNativeStreamRejectsChangingItemType(t *testing.T) {
	const prefix = "{\"type\":\"thread.started\",\"thread_id\":\"session\"}\n{\"type\":\"turn.started\"}\n"
	const fileStart = "{\"type\":\"item.started\",\"item\":{\"id\":\"same\",\"type\":\"file_change\",\"changes\":[{\"path\":\"marker\",\"kind\":\"add\"}],\"status\":\"in_progress\"}}\n"
	const commandEnd = "{\"type\":\"item.completed\",\"item\":{\"id\":\"same\",\"type\":\"command_execution\",\"command\":\"file-change:\\\"marker\\\":\\\"add\\\";\",\"status\":\"completed\",\"exit_code\":0}}\n"
	const suffix = "{\"type\":\"turn.completed\"}\n"
	if stream, err := parseCodexNativeStream([]byte(prefix + fileStart + commandEnd + suffix)); err == nil || len(stream.Commands) != 0 {
		t.Fatalf("file attempt became a command outcome: %+v error=%v", stream, err)
	}
}

func TestCodexNativeStreamRejectsMissingReplayedAndContradictoryEvents(t *testing.T) {
	const session = "{\"type\":\"thread.started\",\"thread_id\":\"session\"}\n"
	const turn = "{\"type\":\"turn.started\"}\n"
	const start = "{\"type\":\"item.started\",\"item\":{\"id\":\"call\",\"type\":\"command_execution\",\"command\":\"touch marker\",\"exit_code\":null,\"status\":\"in_progress\"}}\n"
	const finish = "{\"type\":\"item.completed\",\"item\":{\"id\":\"call\",\"type\":\"command_execution\",\"command\":\"touch marker\",\"exit_code\":0,\"status\":\"completed\"}}\n"
	const done = "{\"type\":\"turn.completed\"}\n"
	for _, test := range []struct{ name, body string }{
		{"empty", ""},
		{"no-session", turn + start + finish + done},
		{"duplicate-session", session + session + turn + done},
		{"no-turn", session + start + finish + done},
		{"duplicate-turn", session + turn + turn + done},
		{"no-attempt", session + turn + finish + done},
		{"duplicate-attempt", session + turn + start + start + finish + done},
		{"no-outcome", session + turn + start + done},
		{"duplicate-outcome", session + turn + start + finish + finish + done},
		{"no-completion", session + turn + start + finish},
		{"truncated", strings.TrimSuffix(session+turn+start+finish+done, "\n")},
		{"late-event", session + turn + done + start},
		{"wrong-command", session + turn + start + strings.Replace(finish, "touch marker", "touch other", 1) + done},
		{"missing-exit", session + turn + start + strings.Replace(finish, `"exit_code":0,`, "", 1) + done},
		{"null-exit", session + turn + start + strings.Replace(finish, `"exit_code":0`, `"exit_code":null`, 1) + done},
		{"contradictory-status", session + turn + start + strings.Replace(finish, `"status":"completed"`, `"status":"failed"`, 1) + done},
		{"case-alias", strings.Replace(session, "thread_id", "Thread_ID", 1) + turn + done},
		{"failed-turn", session + turn + "{\"type\":\"turn.failed\"}\n"},
		{"unqualified-tool", session + turn + "{\"type\":\"item.completed\",\"item\":{\"type\":\"mcp_tool_call\",\"id\":\"extra\"}}\n" + done},
		{"duplicate-json-key", strings.Replace(session, `"thread_id":"session"`, `"thread_id":"first","thread_id":"second"`, 1) + turn + done},
		{"too-many-events", session + turn + strings.Repeat("{\"type\":\"item.completed\",\"item\":{\"type\":\"agent_message\"}}\n", 512) + done},
	} {
		t.Run(test.name, func(t *testing.T) {
			result, err := parseCodexNativeStream([]byte(test.body))
			if err == nil || len(result.Commands) != 0 {
				t.Fatalf("invalid stream retained native evidence: %+v, %v", result, err)
			}
		})
	}
	if _, err := parseCodexNativeStream(bytes.Repeat([]byte{'\n'}, maxHookVerificationOutput+1)); err == nil {
		t.Fatal("oversized stream accepted")
	}
}

func TestCodexNativeRejectionsMatchCapturedHostDiagnostics(t *testing.T) {
	// Exact rejection entries from a real codex-cli 0.154.0 isolated probe.
	body, err := os.ReadFile("testdata/codex-native-hook-rejections.txt")
	if err != nil {
		t.Fatal(err)
	}
	rejections, err := parseCodexNativeRejections(body)
	if err != nil || len(rejections) != 2 {
		t.Fatalf("rejections=%v error=%v", rejections, err)
	}
	for index, test := range []struct{ command, at string }{
		{"*** Begin Patch\n*** Add File: forbidden.txt\n+PROBE_DENIED\n*** End Patch", "2026-09-12T20:08:03.839129Z"},
		{"touch forbidden-command-marker", "2026-09-12T20:08:05.493064Z"},
	} {
		if rejections[index].CommandSHA256 != liveHookDigest([]byte(test.command)) || rejections[index].ObservedAt.Format(time.RFC3339Nano) != test.at {
			t.Fatalf("native rejected command was changed: %+v", rejections[index])
		}
	}
}

func TestCodexNativeRejectionsRejectIncompleteAndForeignFraming(t *testing.T) {
	const native = "2026-09-12T20:08:05.493064Z ERROR codex_core::tools::router: error=Command blocked by PreToolUse hook: denied. Command: touch marker\n"
	for _, test := range []struct {
		name, body string
		count      int
		fails      bool
	}{
		{"missing-terminator", strings.TrimSuffix(native, "\n"), 0, true},
		{"missing-command", strings.Replace(native, " Command: touch marker", "", 1), 0, true},
		{"empty-command", strings.Replace(native, "touch marker", "", 1), 0, true},
		{"ambiguous-command", strings.Replace(native, "touch marker", "touch marker # Command: another", 1), 0, true},
		{"agent-message", `{"type":"agent_message","text":"Command blocked by PreToolUse hook: denied. Command: touch marker"}` + "\n", 0, false},
		{"other-logger", strings.Replace(native, "codex_core::tools::router", "another_logger", 1), 0, false},
		{"wrong-level", strings.Replace(native, "ERROR", "INFO", 1), 0, false},
		{"invalid-time", strings.Replace(native, "2026-09-12", "2026-99-99", 1), 0, false},
		{"unrelated-later-message", native + "2026-09-12T20:09:00Z WARN another_logger: unrelated\n", 1, false},
		{"oversized", strings.Repeat("x", maxHookVerificationOutput+1), 0, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			rejections, err := parseCodexNativeRejections([]byte(test.body))
			if (err != nil) != test.fails || len(rejections) != test.count {
				t.Fatalf("rejections=%v error=%v", rejections, err)
			}
		})
	}
}

func TestCodexNativeDenialRequiresOneCausallyMatchingPolicyDecision(t *testing.T) {
	for _, change := range []string{"valid", "missing-attempt", "missing-policy", "pass-policy", "missing-call", "missing-turn", "missing-tool", "foreign-session", "foreign-run", "wrong-command", "missing-rejection", "duplicate-rejection", "duplicate-command-attempt", "early-rejection", "late-rejection", "missing-prompt", "duplicate-prompt", "foreign-turn", "prompt-after-attempt"} {
		t.Run(change, func(t *testing.T) {
			record, receipt := liveHookBoundCaptureFixture(t)
			record.Binding.PolicyDecision = "block"
			prompt := record
			promptBinding := *record.Binding
			prompt.Binding = &promptBinding
			prompt.Route, prompt.ExitCode, prompt.ResultClass = "codex-user-prompt-submit", 0, "allowed-or-observed"
			promptBinding.Decision, promptBinding.DecisionSource, promptBinding.PolicyDecision = "unproven", "empty-response", "unproven"
			promptBinding.CallSHA256, promptBinding.CommandSHA256 = "", ""
			promptBinding.StartedAt = receipt.StartedAt.Add(time.Millisecond)
			records := []liveHookProbeRecord{prompt, record}
			stream := liveHookCodexStream{SessionSHA256: record.Binding.SessionSHA256}
			rejections := []liveHookNativeRejection{{ObservedAt: time.Now(), CommandSHA256: record.Binding.CommandSHA256}}
			finished := time.Now()
			switch change {
			case "missing-attempt":
				records = nil
			case "missing-policy":
				record.Binding.PolicyDecision = "unproven"
			case "pass-policy":
				record.Binding.PolicyDecision = "pass"
			case "missing-call":
				record.Binding.CallSHA256 = ""
			case "missing-turn":
				record.Binding.TurnSHA256 = ""
			case "missing-tool":
				record.Binding.ToolNameSHA256 = ""
			case "foreign-session":
				stream.SessionSHA256 = liveHookDigest([]byte("other-session"))
			case "foreign-run":
				receipt.RunID = strings.Repeat("b", 32)
			case "wrong-command":
				rejections[0].CommandSHA256 = liveHookDigest([]byte("different command"))
			case "missing-rejection":
				rejections = nil
			case "duplicate-rejection":
				rejections = append(rejections, rejections[0])
			case "duplicate-command-attempt":
				duplicate := record
				binding := *record.Binding
				binding.CallSHA256 = liveHookDigest([]byte("other-call"))
				duplicate.Binding = &binding
				records = append(records, duplicate)
			case "early-rejection":
				rejections[0].ObservedAt = record.Binding.StartedAt.Add(-time.Nanosecond)
			case "late-rejection":
				rejections[0].ObservedAt = finished.Add(time.Nanosecond)
			case "missing-prompt":
				records = []liveHookProbeRecord{record}
			case "duplicate-prompt":
				records = append(records, prompt)
			case "foreign-turn":
				promptBinding.TurnSHA256 = liveHookDigest([]byte("foreign-turn"))
			case "prompt-after-attempt":
				promptBinding.StartedAt = record.Binding.StartedAt.Add(time.Nanosecond)
			}
			call, err := correlateCodexNativeDenial(receipt, records, stream, rejections, "printf private-content", finished)
			if change == "valid" {
				if err != nil || call != record.Binding.CallSHA256 {
					t.Fatalf("matching native evidence rejected: call=%s error=%v", call, err)
				}
			} else if err == nil || call != "" {
				t.Fatalf("unproven native evidence accepted: call=%s error=%v", call, err)
			}
		})
	}
}
