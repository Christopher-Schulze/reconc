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
}

func TestRunPostCompactionDeduplicatesExistingPacket(t *testing.T) {
	repo := t.TempDir()
	t.Setenv(StateRootEnv, t.TempDir())
	result := RunPostCompaction(repo, []byte(`{"session_id":"s1","summary":"already has reconc-context-v1"}`))
	if context := compactionContextFromResult(t, result); context != "" {
		t.Fatalf("duplicate context should be empty, got %q", context)
	}
}

func TestAdaptPostCompactionResultChangesNativeEvent(t *testing.T) {
	result := AdaptPostCompactionResult(
		Result{Stdout: postCompactionJSONOutput("context")},
		"SessionStart",
	)
	if !strings.Contains(result.Stdout, `"hookEventName":"SessionStart"`) {
		t.Fatalf("adapted compaction event missing: %s", result.Stdout)
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
