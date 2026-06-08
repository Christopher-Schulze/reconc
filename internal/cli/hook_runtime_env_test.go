package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestHookRuntimeScopesRunloopUserPromptByAgentRuntime(t *testing.T) {
	repo := bootstrapE2ERepo(t)
	runPrompt := func(event, prompt string) {
		t.Helper()
		payload, err := json.Marshal(map[string]string{
			"session_id": "shared-session",
			"prompt":     prompt,
		})
		if err != nil {
			t.Fatal(err)
		}
		_, stderr, code := runWithStdin(t, string(payload), "hook", "runtime", event, repo)
		if code != 0 {
			t.Fatalf("%s failed: code=%d stderr=%s", event, code, stderr)
		}
	}

	runPrompt("cursor-user-prompt-submit", "/runloop")
	runPrompt("codex-user-prompt-submit", "ist der runloop an?")

	state := readRunloopRuntimeState(t, repo)
	if !state.Enabled || state.Runtime != "cursor" || state.ActiveRunID != "shared-session" {
		t.Fatalf("codex control prompt must not stop cursor runloop, got %+v", state)
	}
	if _, err := os.Stat(filepath.Join(repo, ".reconc", "runloop", "stop")); !os.IsNotExist(err) {
		t.Fatalf("codex control prompt wrote cursor stop marker, stat err=%v", err)
	}

	runPrompt("cursor-user-prompt-submit", "normal cursor prompt")
	state = readRunloopRuntimeState(t, repo)
	if state.Enabled || state.Runtime != "cursor" || state.DisabledReason != "user_prompt" {
		t.Fatalf("cursor normal prompt must stop cursor runloop, got %+v", state)
	}
	marker := readRunloopRuntimeStopMarker(t, repo)
	if marker.Runtime != "cursor" || marker.Reason != "user_prompt" {
		t.Fatalf("cursor stop marker must be runtime scoped, got %+v", marker)
	}
}

type runloopRuntimeState struct {
	Enabled        bool   `json:"enabled"`
	SessionID      string `json:"session_id"`
	ActiveRunID    string `json:"active_run_id"`
	Runtime        string `json:"runtime"`
	DisabledReason string `json:"disabled_reason"`
}

type runloopRuntimeStopMarker struct {
	SessionID   string `json:"session_id"`
	ActiveRunID string `json:"active_run_id"`
	Runtime     string `json:"runtime"`
	Reason      string `json:"reason"`
}

func readRunloopRuntimeState(t *testing.T, repo string) runloopRuntimeState {
	t.Helper()
	body, err := os.ReadFile(filepath.Join(repo, ".reconc", "runloop", "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	var state runloopRuntimeState
	if err := json.Unmarshal(body, &state); err != nil {
		t.Fatal(err)
	}
	return state
}

func readRunloopRuntimeStopMarker(t *testing.T, repo string) runloopRuntimeStopMarker {
	t.Helper()
	body, err := os.ReadFile(filepath.Join(repo, ".reconc", "runloop", "stop"))
	if err != nil {
		t.Fatal(err)
	}
	var marker runloopRuntimeStopMarker
	if err := json.Unmarshal(body, &marker); err != nil {
		t.Fatal(err)
	}
	return marker
}
