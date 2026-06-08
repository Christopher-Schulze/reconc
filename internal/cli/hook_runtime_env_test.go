package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestHookRuntimeScopesDegenmodeUserPromptByAgentRuntime(t *testing.T) {
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

	runPrompt("cursor-user-prompt-submit", "/degenmode")
	runPrompt("codex-user-prompt-submit", "ist der degenmode an?")

	state := readDegenmodeRuntimeState(t, repo)
	if !state.Enabled || state.Runtime != "cursor" || state.ActiveRunID != "shared-session" {
		t.Fatalf("codex control prompt must not stop cursor degenmode, got %+v", state)
	}
	if _, err := os.Stat(filepath.Join(repo, ".reconc", "degenmode", "stop")); !os.IsNotExist(err) {
		t.Fatalf("codex control prompt wrote cursor stop marker, stat err=%v", err)
	}

	runPrompt("cursor-user-prompt-submit", "normal cursor prompt")
	state = readDegenmodeRuntimeState(t, repo)
	if state.Enabled || state.Runtime != "cursor" || state.DisabledReason != "user_prompt" {
		t.Fatalf("cursor normal prompt must stop cursor degenmode, got %+v", state)
	}
	marker := readDegenmodeRuntimeStopMarker(t, repo)
	if marker.Runtime != "cursor" || marker.Reason != "user_prompt" {
		t.Fatalf("cursor stop marker must be runtime scoped, got %+v", marker)
	}
}

type degenmodeRuntimeState struct {
	Enabled        bool   `json:"enabled"`
	SessionID      string `json:"session_id"`
	ActiveRunID    string `json:"active_run_id"`
	Runtime        string `json:"runtime"`
	DisabledReason string `json:"disabled_reason"`
}

type degenmodeRuntimeStopMarker struct {
	SessionID   string `json:"session_id"`
	ActiveRunID string `json:"active_run_id"`
	Runtime     string `json:"runtime"`
	Reason      string `json:"reason"`
}

func readDegenmodeRuntimeState(t *testing.T, repo string) degenmodeRuntimeState {
	t.Helper()
	body, err := os.ReadFile(filepath.Join(repo, ".reconc", "degenmode", "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	var state degenmodeRuntimeState
	if err := json.Unmarshal(body, &state); err != nil {
		t.Fatal(err)
	}
	return state
}

func readDegenmodeRuntimeStopMarker(t *testing.T, repo string) degenmodeRuntimeStopMarker {
	t.Helper()
	body, err := os.ReadFile(filepath.Join(repo, ".reconc", "degenmode", "stop"))
	if err != nil {
		t.Fatal(err)
	}
	var marker degenmodeRuntimeStopMarker
	if err := json.Unmarshal(body, &marker); err != nil {
		t.Fatal(err)
	}
	return marker
}
