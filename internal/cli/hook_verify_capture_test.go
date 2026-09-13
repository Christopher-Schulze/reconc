package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLiveHookCaptureClassifiesActualRuntimeDecisions(t *testing.T) {
	repo := bootstrapE2ERepo(t)
	for _, test := range []struct {
		name, route, payload, decision, source string
		code                                   int
	}{
		{"codex-deny", "codex-pre-tool-use", `{"session_id":"capture-codex-deny","tool_name":"apply_patch","tool_input":{"command":"*** Begin Patch\n*** Add File: generated/denied.txt\n+denied\n*** End Patch"}}`, "deny", "exit-2", 2},
		{"cursor-deny", "cursor-pre-tool-use", `{"conversation_id":"capture-cursor-deny","hook_event_name":"preToolUse","tool_name":"Write","tool_input":{"file_path":"generated/denied.txt"}}`, "deny", "cursor-permission", 0},
		{"cursor-allow", "cursor-pre-tool-use", `{"conversation_id":"capture-cursor-allow","hook_event_name":"preToolUse","tool_name":"Write","tool_input":{"file_path":"allowed.txt"}}`, "allow", "cursor-permission", 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			err := runHookRuntimeWithInput([]string{test.route, repo}, strings.NewReader(test.payload), &stdout, &stderr)
			code := ExitCode(err)
			decision, source, class := classifyLiveHookResponse(test.route, code, stdout.Bytes())
			if code != test.code || decision != test.decision || source != test.source {
				t.Fatalf("exit=%d decision=%s source=%s stdout=%s stderr=%s", code, decision, source, &stdout, &stderr)
			}
			record, err := newLiveHookCaptureRecord(strings.Repeat("a", 32), test.route, []byte(test.payload))
			if err != nil {
				t.Fatal(err)
			}
			record.ExitCode, record.ResultClass = code, class
			record.Binding.Decision, record.Binding.DecisionSource, record.Binding.ResponseSHA256 = decision, source, liveHookDigest(stdout.Bytes())
			if !validLiveHookProbeRecord(record) {
				t.Fatalf("real decision was rejected: %+v", record)
			}
		})
	}
}

func TestLiveHookCaptureDoesNotInventDecisionsFromForeignJSON(t *testing.T) {
	for _, test := range []struct{ route, body string }{
		{"codex-post-tool-use", `{"permission":"deny"}`},
		{"codex-pre-tool-use", `{"permission":"deny"}`},
		{"cursor-post-tool-use", `{"permission":"deny"}`},
		{"cursor-pre-tool-use", `{"decision":"deny"}`},
		{"codex-pre-tool-use", `{"hookSpecificOutput":{"hookEventName":"PostToolUse","permissionDecision":"deny"}}`},
		{"codex-pre-tool-use", `{"hookSpecificOutput":{"permissionDecision":"deny"}}`},
		{"cursor-pre-tool-use", `{"permission":false}`},
		{"cursor-pre-tool-use", `{"permission":"deny"} {"permission":"allow"}`},
		{"cursor-pre-tool-use", `{"permission":"allow","permission":"deny"}`},
		{"cursor-pre-tool-use", `{"Permission":"deny"}`},
		{"cursor-pre-tool-use", `{"permission":"unknown","Permission":"deny"}`},
		{"codex-pre-tool-use", `{"HookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny"}}`},
		{"codex-pre-tool-use", `{"hookSpecificOutput":{"HookEventName":"PreToolUse","permissionDecision":"deny"}}`},
		{"codex-pre-tool-use", `{"hookSpecificOutput":{"hookEventName":"PreToolUse","PermissionDecision":"deny"}}`},
		{"cursor-pre-tool-use", `not-json`},
	} {
		decision, _, class := classifyLiveHookResponse(test.route, 0, []byte(test.body))
		if decision != "unproven" || class != "allowed-or-observed" {
			t.Fatalf("foreign response promoted: %s %s -> %s %s", test.route, test.body, decision, class)
		}
	}
}

func liveHookBoundCaptureFixture(t *testing.T) (liveHookProbeRecord, *liveHookReceipt) {
	t.Helper()
	receipt := &liveHookReceipt{RunID: strings.Repeat("a", 32), StartedAt: time.Now().Add(-time.Second)}
	payload := []byte(`{"session_id":"private-session","turn_id":"private-turn","tool_use_id":"private-call","tool_name":"Bash","tool_input":{"command":"printf private-content"},"prompt":"private-prompt"}`)
	record, err := newLiveHookCaptureRecord(receipt.RunID, "codex-pre-tool-use", payload)
	if err != nil {
		t.Fatal(err)
	}
	record.ExitCode, record.ResultClass = 2, "blocked"
	record.Binding.Decision, record.Binding.DecisionSource = "deny", "exit-2"
	record.Binding.ResponseSHA256 = liveHookDigest([]byte(`{"decision":"block"}`))
	return record, receipt
}

func TestLiveHookCaptureRedactsAndBindsDeliveredInput(t *testing.T) {
	record, receipt := liveHookBoundCaptureFixture(t)
	if record.Binding.SessionSHA256 != liveHookDigest([]byte("private-session")) || record.Binding.CallSHA256 != liveHookDigest([]byte("private-call")) || record.Binding.ToolNameSHA256 != liveHookDigest([]byte("Bash")) || record.Binding.ToolInputSHA256 != liveHookDigest([]byte(`{"command":"printf private-content"}`)) {
		t.Fatalf("missing actual source identity: %+v", record.Binding)
	}
	if record.Binding.TurnSHA256 != liveHookDigest([]byte("private-turn")) || record.Binding.PolicyDecision != "unproven" {
		t.Fatal("missing native turn binding or invented policy provenance")
	}
	repo := t.TempDir()
	if err := os.Mkdir(filepath.Join(repo, ".reconc"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := appendLiveHookCapture(repo, record); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(filepath.Join(repo, ".reconc/hook-verify-events.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	for _, private := range []string{"private-session", "private-turn", "private-call", "private-content", "private-prompt", "printf"} {
		if bytes.Contains(body, []byte(private)) {
			t.Fatalf("capture leaked %s", private)
		}
	}
	records, err := readLiveHookProbeRecords(repo)
	if err != nil || len(records) != 1 {
		t.Fatalf("capture read: %v, records=%v", err, records)
	}
	if err := validateLiveHookCaptureBindings(records, receipt, time.Now()); err != nil {
		t.Fatal(err)
	}
	result := applyLiveHookProbeRecords(hookVerificationResult{}, records, repo)
	if result.Enforced || !result.Observed || !result.Degraded {
		t.Fatal("hook input was promoted to native enforcement")
	}
}

func TestLiveHookCaptureRejectsStaleReplayedAndContradictoryRecords(t *testing.T) {
	for _, change := range []string{"missing-binding", "foreign-run", "before-run", "future-record", "impossible-duration", "expired-run", "duplicate-call", "invalid-call", "foreign-source", "wrong-exit", "invalid-field"} {
		t.Run(change, func(t *testing.T) {
			record, receipt := liveHookBoundCaptureFixture(t)
			switch change {
			case "missing-binding":
				record.Binding = nil
			case "foreign-run":
				record.Binding.RunID = strings.Repeat("b", 32)
			case "before-run":
				record.Binding.StartedAt = receipt.StartedAt.Add(-time.Second)
			case "future-record":
				record.Binding.StartedAt = time.Now().Add(time.Second)
			case "impossible-duration":
				record.DurationNanos = int64(time.Hour)
			case "expired-run":
				receipt.StartedAt = time.Now().Add(-6 * time.Minute)
			case "invalid-call":
				record.Binding.CallSHA256 = "private-call"
			case "foreign-source":
				record.ExitCode, record.Binding.DecisionSource = 0, "cursor-permission"
			case "wrong-exit":
				record.ExitCode = 0
			case "invalid-field":
				record.Fields = []string{" "}
			}
			records := []liveHookProbeRecord{record}
			if change == "duplicate-call" {
				records = append(records, record)
			}
			if err := validateLiveHookCaptureBindings(records, receipt, time.Now()); err == nil {
				t.Fatal("invalid capture accepted")
			}
		})
	}
}

func TestLiveHookCaptureDistinguishesCursorToolsWithReusedCallID(t *testing.T) {
	receipt := &liveHookReceipt{RunID: strings.Repeat("a", 32), StartedAt: time.Now().Add(-time.Second)}
	first, err := newLiveHookCaptureRecord(receipt.RunID, "cursor-post-tool-use-failure", []byte(`{"session_id":"cursor-session","tool_use_id":"shared-call","tool_name":"write","tool_input":{"file_path":"forbidden.txt"}}`))
	if err != nil {
		t.Fatal(err)
	}
	second, err := newLiveHookCaptureRecord(receipt.RunID, "cursor-post-tool-use-failure", []byte(`{"session_id":"cursor-session","tool_use_id":"shared-call","tool_name":"bash","tool_input":{"command":"exit 7"}}`))
	if err != nil {
		t.Fatal(err)
	}
	for _, record := range []*liveHookProbeRecord{&first, &second} {
		record.ResultClass = "allowed-or-observed"
		record.Binding.Decision, record.Binding.DecisionSource = "unproven", "empty-response"
		record.Binding.ResponseSHA256 = liveHookDigest(nil)
	}
	if err := validateLiveHookCaptureBindings([]liveHookProbeRecord{first, second}, receipt, time.Now()); err != nil {
		t.Fatalf("distinct Cursor tools reused one tool_use_id: %v", err)
	}
	if err := validateLiveHookCaptureBindings([]liveHookProbeRecord{first, first}, receipt, time.Now()); err == nil {
		t.Fatal("duplicate native tool identity was accepted")
	}
}

func TestLiveHookCaptureCommandUsesExactNativeKey(t *testing.T) {
	for _, test := range []struct{ input, command string }{
		{`{"command":"touch exact"}`, "touch exact"},
		{`{"Command":"touch alias"}`, ""},
		{`{"command":"touch exact","Command":"touch alias"}`, "touch exact"},
		{`{"Command":"touch alias","command":"touch exact"}`, "touch exact"},
		{`{"command":false}`, ""},
	} {
		record, err := newLiveHookCaptureRecord(strings.Repeat("a", 32), "codex-pre-tool-use", []byte(`{"tool_input":`+test.input+`}`))
		if err != nil {
			t.Fatal(err)
		}
		want := ""
		if test.command != "" {
			want = liveHookDigest([]byte(test.command))
		}
		if record.Binding.CommandSHA256 != want {
			t.Fatalf("input=%s command digest=%s want=%s", test.input, record.Binding.CommandSHA256, want)
		}
	}
}

func TestLiveHookCaptureRejectsMalformedAndOversizedObjects(t *testing.T) {
	for _, payload := range []string{"null", "[]", "false", "{} {}", `{"session_id":"a","session_id":"b"}`, `{"tool_input":{"command":"allow","command":"deny"}}`, `{"` + strings.Repeat("x", 129) + `":0}`} {
		if _, err := newLiveHookCaptureRecord(strings.Repeat("a", 32), "codex-pre-tool-use", []byte(payload)); err == nil {
			t.Fatalf("accepted malformed payload %q", payload)
		}
	}
	fields := make(map[string]int, 65)
	for index := range 65 {
		fields[strings.Repeat("x", index+1)] = index
	}
	payload, err := json.Marshal(fields)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := newLiveHookCaptureRecord(strings.Repeat("a", 32), "codex-pre-tool-use", payload); err == nil {
		t.Fatal("oversized field set accepted")
	}
}

func TestLiveHookCaptureReaderRejectsAmbiguousAndOversizedRecords(t *testing.T) {
	for _, body := range []string{
		`{"route":"codex-pre-tool-use","route":"codex-stop","fields":[],"result_class":"blocked","exit_code":2,"duration_ns":0}`,
		`{"Route":"codex-pre-tool-use","fields":[],"result_class":"blocked","exit_code":2,"duration_ns":0}`,
		strings.Repeat(" ", 64*1024+1),
	} {
		repo := t.TempDir()
		if err := os.Mkdir(filepath.Join(repo, ".reconc"), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(repo, ".reconc/hook-verify-events.jsonl"), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := readLiveHookProbeRecords(repo); err == nil {
			t.Fatal("ambiguous or oversized capture accepted")
		}
	}
}
