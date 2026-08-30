package agentsession

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunPostCompactionReturnsBoundedRecoveryContext(t *testing.T) {
	repo := t.TempDir()
	t.Setenv(StateRootEnv, t.TempDir())
	if err := os.MkdirAll(filepath.Join(repo, "docs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "docs", "tasks.md"), []byte("# Tasks\n\n## Active\n\n- [~] 005 Hook registry -> tasks/005-hook.md\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := InitializeSessionState(repo, "s1"); err != nil {
		t.Fatal(err)
	}
	result := RunPostCompaction(repo, []byte(`{"session_id":"s1","summary":"short"}`))
	if result.ExitCode != 0 || result.Stderr != "" {
		t.Fatalf("unexpected result: %#v", result)
	}
	if len(result.Stdout) > maxCompactionContextBytes+512 {
		t.Fatalf("compaction response is not bounded: %d bytes", len(result.Stdout))
	}
	context := compactionContextFromResult(t, result)
	for _, token := range []string{compactionContextMarker, "Active task: - [~] 005 Hook registry", "Session evidence:", "Re-run relevant verification"} {
		if !strings.Contains(context, token) {
			t.Fatalf("context missing %q: %s", token, context)
		}
	}
	if !hasCompactionRecoveryEnvelope(context) {
		t.Fatalf("compaction context is not a valid recovery envelope: %s", context)
	}
}

func TestRunPostCompactionDeduplicatesExistingPacket(t *testing.T) {
	repo := t.TempDir()
	t.Setenv(StateRootEnv, t.TempDir())
	packet := compactionRecoveryEnvelope("preserved recovery\nUnicode: Wiederaufnahme ✓")
	payload, err := json.Marshal(map[string]interface{}{"session_id": "s1", "summary": "host summary\n" + packet})
	if err != nil {
		t.Fatal(err)
	}
	result := RunPostCompaction(repo, payload)
	if context := compactionContextFromResult(t, result); context != "" {
		t.Fatalf("duplicate context should be empty, got %q", context)
	}
}

func TestCompactionRecoveryEnvelopeRejectsMarkerLikeAndMalformedText(t *testing.T) {
	packet := compactionRecoveryEnvelope("line one\nUnicode: Καλημέρα 世界")
	beginLine := strings.SplitN(packet, "\n", 2)[0]
	cases := []struct {
		name    string
		summary string
		want    bool
	}{
		{name: "exact final packet", summary: packet, want: true},
		{name: "multiline prefix then packet", summary: "ordinary summary\nsecond line\n" + packet, want: true},
		{name: "bounded long prefix", summary: strings.Repeat("x", 2*maxCompactionSummaryScan) + "\n" + packet, want: true},
		{name: "duplicate genuine packets", summary: packet + "\n" + packet, want: true},
		{name: "marker prose", summary: "already has " + compactionContextMarker},
		{name: "path", summary: "/tmp/" + compactionContextMarker + "/notes"},
		{name: "quoted payload", summary: `{"message":"` + compactionContextMarker + `"}`},
		{name: "prefix collision", summary: "x" + packet},
		{name: "suffix collision", summary: packet + "x"},
		{name: "truncated", summary: packet[:len(packet)-1]},
		{name: "begin only", summary: beginLine + "\nbody"},
		{name: "malformed digest", summary: strings.Replace(packet, "sha256=", "sha256=Z", 1)},
		{name: "packet followed by prose", summary: packet + "\nquoted later"},
		{name: "packet outside bounded tail", summary: packet + strings.Repeat(" ", maxCompactionSummaryScan+1)},
		{name: "code marker", summary: "```text\n" + compactionContextMarker + "\n```"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			if got := hasCompactionRecoveryEnvelope(test.summary); got != test.want {
				t.Fatalf("envelope detection = %t, want %t\n%s", got, test.want, test.summary)
			}
		})
	}
}

func TestCompactionRecoveryEnvelopeRemainsValidWhenBounded(t *testing.T) {
	body := strings.Repeat("Wiederaufnahme ✓\n", maxCompactionContextBytes)
	packet := compactionRecoveryEnvelope(body)
	if len(packet) > maxCompactionContextBytes {
		t.Fatalf("packet bytes = %d, want <= %d", len(packet), maxCompactionContextBytes)
	}
	if !strings.Contains(packet, "[reconc context truncated]") || !hasCompactionRecoveryEnvelope(packet) {
		t.Fatalf("bounded packet is not valid: %q", packet)
	}
}

func TestAdaptPostCompactionResultChangesNativeEvent(t *testing.T) {
	body, err := postCompactionJSONOutput("context")
	if err != nil {
		t.Fatal(err)
	}
	result := AdaptPostCompactionResult(
		Result{Stdout: body},
		"SessionStart",
	)
	if !strings.Contains(result.Stdout, `"hookEventName":"SessionStart"`) {
		t.Fatalf("adapted compaction event missing: %s", result.Stdout)
	}
}

func TestAdaptCodexCompactionResultUsesSystemMessage(t *testing.T) {
	body, err := postCompactionJSONOutput("context")
	if err != nil {
		t.Fatal(err)
	}
	result := AdaptCodexCompactionResult(Result{Stdout: body})
	if result.Stdout != `{"systemMessage":"context"}` {
		t.Fatalf("Codex compaction output = %s", result.Stdout)
	}
}

func compactionContextFromResult(t *testing.T, result Result) string {
	t.Helper()
	var body map[string]interface{}
	if err := json.Unmarshal([]byte(result.Stdout), &body); err != nil {
		t.Fatalf("invalid result JSON: %v: %s", err, result.Stdout)
	}
	hookOutput, _ := body["hookSpecificOutput"].(map[string]interface{})
	context, _ := hookOutput["additionalContext"].(string)
	return context
}
