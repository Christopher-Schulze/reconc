package agentsession

import (
	"os"
	"testing"
)

func TestReconcileRunLoopStopDisablesOnInterruptErrorHint(t *testing.T) {
	_, repo := withStateRoot(t)

	if err := reconcileRunLoopState(repo, "session-1", &HookPayload{SessionID: "session-1"}, runLoopSessionStart); err != nil {
		t.Fatalf("session start: %v", err)
	}
	if err := reconcileRunLoopState(repo, "session-1",
		&HookPayload{
			SessionID: "session-1",
			Prompt:    "/runloop",
		},
		runLoopUserPrompt,
	); err != nil {
		t.Fatalf("user prompt: %v", err)
	}

	before, err := loadRunLoopState(repo)
	if err != nil {
		t.Fatal(err)
	}
	if !before.Enabled || before.ActiveRunID != "session-1" {
		t.Fatalf("expected enabled run state, got %+v", before)
	}

	yes := true
	if err := reconcileRunLoopState(repo, "session-1",
		&HookPayload{
			SessionID:   "session-1",
			IsInterrupt: &yes,
		},
		runLoopStopEvent,
	); err != nil {
		t.Fatalf("stop event: %v", err)
	}

	after, err := loadRunLoopState(repo)
	if err != nil {
		t.Fatal(err)
	}
	if after.Enabled {
		t.Fatalf("expected disabled state after interrupt")
	}
	if after.DisabledReason != "user_interrupt" {
		t.Fatalf("expected disabled_reason=user_interrupt, got %q", after.DisabledReason)
	}
	if after.ActiveRunID != "" {
		t.Fatalf("expected active_run_id reset on stop interrupt")
	}
}

func TestReconcileRunLoopStopPreservesEnabledWithoutInterrupt(t *testing.T) {
	_, repo := withStateRoot(t)
	if err := reconcileRunLoopState(repo, "session-2", &HookPayload{SessionID: "session-2"}, runLoopSessionStart); err != nil {
		t.Fatalf("session start: %v", err)
	}
	if err := reconcileRunLoopState(repo, "session-2",
		&HookPayload{
			SessionID: "session-2",
			Prompt:    "/runloop",
		},
		runLoopUserPrompt,
	); err != nil {
		t.Fatalf("user prompt: %v", err)
	}

	if err := reconcileRunLoopState(repo, "session-2",
		&HookPayload{
			SessionID: "session-2",
			Error:     "session complete",
			Raw:       map[string]interface{}{"error": "session complete"},
		},
		runLoopStopEvent,
	); err != nil {
		t.Fatalf("stop event: %v", err)
	}

	state, err := loadRunLoopState(repo)
	if err != nil {
		t.Fatal(err)
	}
	if !state.Enabled {
		t.Fatalf("expected runloop to stay enabled for non-interrupt stop payload")
	}
	if state.DisabledReason != "" {
		t.Fatalf("unexpected disabled reason: %q", state.DisabledReason)
	}
}

func TestReconcileRunLoopStopTextDoesNotActAsOffSwitch(t *testing.T) {
	_, repo := withStateRoot(t)
	if err := reconcileRunLoopState(repo, "session-3", &HookPayload{SessionID: "session-3"}, runLoopSessionStart); err != nil {
		t.Fatalf("session start: %v", err)
	}
	if err := reconcileRunLoopState(repo, "session-3",
		&HookPayload{
			SessionID: "session-3",
			Prompt:    "/runloop",
		},
		runLoopUserPrompt,
	); err != nil {
		t.Fatalf("user prompt: %v", err)
	}

	if err := reconcileRunLoopState(repo, "session-3",
		&HookPayload{
			SessionID: "session-3",
			ToolInput: map[string]interface{}{"command": "runLoop mode off"},
		},
		runLoopToolEvent,
	); err != nil {
		t.Fatalf("tool event: %v", err)
	}

	state, err := loadRunLoopState(repo)
	if err != nil {
		t.Fatal(err)
	}
	if !state.Enabled {
		t.Fatalf("expected runloop to stay enabled; text off is not a runtime stop command")
	}
	if state.DisabledReason != "" {
		t.Fatalf("unexpected disabled_reason %q", state.DisabledReason)
	}
}

func TestReconcileRunLoopOtherSessionPromptDoesNotStopActiveRun(t *testing.T) {
	_, repo := withStateRoot(t)
	if err := reconcileRunLoopState(repo, "cursor-run", &HookPayload{SessionID: "cursor-run", Prompt: "/runloop mach weiter"}, runLoopUserPrompt); err != nil {
		t.Fatalf("enable: %v", err)
	}
	if err := reconcileRunLoopState(repo, "codex-side-chat", &HookPayload{SessionID: "codex-side-chat", Prompt: "normal diagnostic prompt"}, runLoopUserPrompt); err != nil {
		t.Fatalf("other prompt: %v", err)
	}
	state, err := loadRunLoopState(repo)
	if err != nil {
		t.Fatal(err)
	}
	if !state.Enabled || state.ActiveRunID != "cursor-run" {
		t.Fatalf("other session prompt must not stop active run, got %+v", state)
	}
	if hasRunLoopStopFile(repo) {
		t.Fatal("other session prompt must not write repo-global stopfile")
	}
}

func TestReconcileRunLoopSameSessionPromptStopsActiveRun(t *testing.T) {
	_, repo := withStateRoot(t)
	if err := reconcileRunLoopState(repo, "cursor-run", &HookPayload{SessionID: "cursor-run", Prompt: "/runloop"}, runLoopUserPrompt); err != nil {
		t.Fatalf("enable: %v", err)
	}
	if err := reconcileRunLoopState(repo, "cursor-run", &HookPayload{SessionID: "cursor-run", Prompt: "normal follow-up"}, runLoopUserPrompt); err != nil {
		t.Fatalf("same prompt: %v", err)
	}
	state, err := loadRunLoopState(repo)
	if err != nil {
		t.Fatal(err)
	}
	if state.Enabled || state.DisabledReason != "user_prompt" {
		t.Fatalf("same session normal prompt must stop active run, got %+v", state)
	}
	if !hasRunLoopStopFile(repo) {
		t.Fatal("same session normal prompt must write scoped stopfile")
	}
}

func TestReconcileRunLoopBtwPromptPreservesActiveRun(t *testing.T) {
	_, repo := withStateRoot(t)
	if err := reconcileRunLoopStateForRuntime(repo, "claude-run", "claude", &HookPayload{SessionID: "claude-run", Prompt: "/runloop"}, runLoopUserPrompt); err != nil {
		t.Fatalf("enable: %v", err)
	}
	if err := reconcileRunLoopStateForRuntime(repo, "claude-run", "claude", &HookPayload{SessionID: "claude-run", Prompt: "/btw Kontroll-Update: weiterarbeiten, nicht stoppen"}, runLoopUserPrompt); err != nil {
		t.Fatalf("btw prompt: %v", err)
	}
	state, err := loadRunLoopState(repo)
	if err != nil {
		t.Fatal(err)
	}
	if !state.Enabled || state.ActiveRunID != "claude-run" || state.DisabledReason != "" {
		t.Fatalf("/btw side-channel prompt must preserve active run, got %+v", state)
	}
	if hasRunLoopStopFile(repo) {
		t.Fatal("/btw side-channel prompt must not write a stopfile")
	}
}

func TestReconcileRunLoopBtwPrefixWithoutBoundaryStopsActiveRun(t *testing.T) {
	_, repo := withStateRoot(t)
	if err := reconcileRunLoopStateForRuntime(repo, "claude-run", "claude", &HookPayload{SessionID: "claude-run", Prompt: "/runloop"}, runLoopUserPrompt); err != nil {
		t.Fatalf("enable: %v", err)
	}
	if err := reconcileRunLoopStateForRuntime(repo, "claude-run", "claude", &HookPayload{SessionID: "claude-run", Prompt: "/btwgo is not the side-channel command"}, runLoopUserPrompt); err != nil {
		t.Fatalf("btw prefix prompt: %v", err)
	}
	state, err := loadRunLoopState(repo)
	if err != nil {
		t.Fatal(err)
	}
	if state.Enabled || state.DisabledReason != "user_prompt" {
		t.Fatalf("/btw prefix without boundary must behave as a normal user stop, got %+v", state)
	}
	if !hasRunLoopStopFile(repo) {
		t.Fatal("/btw prefix without boundary must write scoped stopfile")
	}
}

func TestReconcileRunLoopRuntimeInternalPromptDoesNotStopActiveRun(t *testing.T) {
	_, repo := withStateRoot(t)
	if err := reconcileRunLoopStateForRuntime(repo, "cursor-run", "cursor", &HookPayload{SessionID: "cursor-run", Prompt: "arbeite autonom /runloop"}, runLoopUserPrompt); err != nil {
		t.Fatalf("enable: %v", err)
	}
	internalPrompts := []string{
		"runloop autocontinue. Continue the repository task lifecycle without asking for routine permission.\n\nState:\n- Current TASK: TASK-0219-Model-Quality-Tracker\n- Active Sub-Task: Read current implementation and understand exact gap\n",
		"Briefly inform the user about the task result and perform any follow-up actions (if needed). If there's no follow-ups needed, don't explicitly say that.",
		"The above subagent result is already visible to the user. DO NOT reiterate or summarize its contents unless asked, or if multi-task result synthesis is required. Otherwise end your response with a brief third-person confirmation that the subagent has completed. Don't repeat the same confirmation every time.",
	}
	for _, prompt := range internalPrompts {
		if err := reconcileRunLoopStateForRuntime(repo, "cursor-run", "cursor", &HookPayload{SessionID: "cursor-run", Prompt: prompt}, runLoopUserPrompt); err != nil {
			t.Fatalf("internal prompt: %v", err)
		}
		state, err := loadRunLoopState(repo)
		if err != nil {
			t.Fatal(err)
		}
		if !state.Enabled || state.ActiveRunID != "cursor-run" || state.DisabledReason != "" {
			t.Fatalf("runtime-internal prompt must preserve active run, got %+v", state)
		}
		if hasRunLoopStopFile(repo) {
			t.Fatal("runtime-internal prompt must not write a stopfile")
		}
	}
}

func TestReconcileRunLoopPureHookPromptDoesNotWriteStopFile(t *testing.T) {
	_, repo := withStateRoot(t)
	prompt := `<hook_prompt hook_run_id="stop:5:/repo/.codex/hooks.json">runloop autocontinue. Continue the repository task lifecycle without asking for routine permission.</hook_prompt>`

	if err := reconcileRunLoopStateForRuntime(repo, "codex-run", "codex", &HookPayload{SessionID: "codex-run", Prompt: prompt}, runLoopUserPrompt); err != nil {
		t.Fatalf("hook prompt: %v", err)
	}
	state, err := loadRunLoopState(repo)
	if err != nil {
		t.Fatal(err)
	}
	if state.Enabled || state.DisabledReason != "" {
		t.Fatalf("pure hook prompt must be ignored without disabling state, got %+v", state)
	}
	if hasRunLoopStopFile(repo) {
		t.Fatal("pure hook prompt must not write a stopfile")
	}
}

func TestReconcileRunLoopDiagnosticAutocontinueMentionStopsActiveRun(t *testing.T) {
	_, repo := withStateRoot(t)
	if err := reconcileRunLoopStateForRuntime(repo, "codex-run", "codex", &HookPayload{SessionID: "codex-run", Prompt: "/runloop"}, runLoopUserPrompt); err != nil {
		t.Fatalf("enable: %v", err)
	}
	if err := reconcileRunLoopStateForRuntime(repo, "codex-run", "codex", &HookPayload{SessionID: "codex-run", Prompt: "warum kam runloop autocontinue gerade?"}, runLoopUserPrompt); err != nil {
		t.Fatalf("diagnostic user prompt: %v", err)
	}
	state, err := loadRunLoopState(repo)
	if err != nil {
		t.Fatal(err)
	}
	if state.Enabled || state.DisabledReason != "user_prompt" {
		t.Fatalf("real diagnostic user prompt must stop active run, got %+v", state)
	}
	if !hasRunLoopStopFile(repo) {
		t.Fatal("real diagnostic user prompt must write scoped stopfile")
	}
}

func TestReconcileRunLoopOtherSessionInterruptDoesNotStopActiveRun(t *testing.T) {
	_, repo := withStateRoot(t)
	if err := reconcileRunLoopState(repo, "cursor-run", &HookPayload{SessionID: "cursor-run", Prompt: "/runloop"}, runLoopUserPrompt); err != nil {
		t.Fatalf("enable: %v", err)
	}
	yes := true
	if err := reconcileRunLoopState(repo, "codex-side-chat", &HookPayload{SessionID: "codex-side-chat", IsInterrupt: &yes}, runLoopStopEvent); err != nil {
		t.Fatalf("other interrupt: %v", err)
	}
	state, err := loadRunLoopState(repo)
	if err != nil {
		t.Fatal(err)
	}
	if !state.Enabled || state.ActiveRunID != "cursor-run" {
		t.Fatalf("other session interrupt must not stop active run, got %+v", state)
	}
}

func TestIsUserStopInterruptDetectsSignals(t *testing.T) {
	yes := true
	no := false
	tests := []struct {
		name    string
		payload HookPayload
		want    bool
	}{
		{name: "is_interrupt true", payload: HookPayload{SessionID: "s", IsInterrupt: &yes}, want: true},
		{name: "is_interrupt false", payload: HookPayload{SessionID: "s", IsInterrupt: &no}, want: false},
		{name: "is_interrupt nil", payload: HookPayload{SessionID: "s"}, want: false},
		{name: "error text ignored", payload: HookPayload{SessionID: "s", Error: "user aborted"}, want: false},
		{name: "raw text ignored", payload: HookPayload{SessionID: "s", Raw: map[string]interface{}{"status": "command terminated"}}, want: false},
		{name: "clean stop", payload: HookPayload{SessionID: "s", Error: "session complete"}, want: false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := isUserStopInterrupt(&tc.payload); got != tc.want {
				t.Fatalf("isUserStopInterrupt() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestReconcileRunLoopInterruptBlocksEnableIntent(t *testing.T) {
	_, repo := withStateRoot(t)
	if err := reconcileRunLoopState(repo, "session-5", &HookPayload{SessionID: "session-5"}, runLoopSessionStart); err != nil {
		t.Fatalf("session start: %v", err)
	}
	if err := reconcileRunLoopState(repo, "session-5",
		&HookPayload{
			SessionID: "session-5",
			Prompt:    "/runloop",
		},
		runLoopUserPrompt,
	); err != nil {
		t.Fatalf("user prompt: %v", err)
	}

	// Stop event: BOTH an interrupt flag AND "runloop" in tool input.
	yes := true
	if err := reconcileRunLoopState(repo, "session-5",
		&HookPayload{
			SessionID:   "session-5",
			IsInterrupt: &yes,
			ToolInput:   map[string]interface{}{"command": "runloop"},
		},
		runLoopStopEvent,
	); err != nil {
		t.Fatalf("stop event: %v", err)
	}

	state, err := loadRunLoopState(repo)
	if err != nil {
		t.Fatal(err)
	}
	if state.Enabled {
		t.Fatalf("expected runloop to stay disabled: interrupt must block enable intent")
	}
	if state.DisabledReason != "user_interrupt" {
		t.Fatalf("expected disabled_reason=user_interrupt when no disable intent, got %q", state.DisabledReason)
	}
}

func TestLoadRunLoopStateMissingReturnsZero(t *testing.T) {
	_, repo := withStateRoot(t)
	state, err := loadRunLoopState(repo)
	if err != nil {
		t.Fatalf("loadRunLoopState: %v", err)
	}
	if state.Enabled {
		t.Fatal("expected disabled state for missing file")
	}
	if state.SessionID != "" || state.ActiveRunID != "" || state.DisabledReason != "" || state.StopAnchorMessageID != "" {
		t.Fatalf("expected all empty fields, got %+v", state)
	}
}

func TestLoadRunLoopStateHandlesNegativeNudges(t *testing.T) {
	_, repo := withStateRoot(t)
	stateDir := repo + "/.reconc/runloop"
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(stateDir+"/state.json", []byte(`{"enabled":true,"session_id":"s","no_progress_nudges":-5}`), 0o644); err != nil {
		t.Fatal(err)
	}
	state, err := loadRunLoopState(repo)
	if err != nil {
		t.Fatalf("loadRunLoopState: %v", err)
	}
	if state.NoProgressNudges != 0 {
		t.Fatalf("expected negative no_progress_nudges normalized to 0, got %d", state.NoProgressNudges)
	}
}

func TestLoadRunLoopStateCorruptJSON(t *testing.T) {
	_, repo := withStateRoot(t)
	stateDir := repo + "/.reconc/runloop"
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(stateDir+"/state.json", []byte(`{not valid json at all`), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := loadRunLoopState(repo)
	if err == nil {
		t.Fatal("expected error for corrupt JSON")
	}
}

func TestSaveAndLoadRunLoopStateRoundtrip(t *testing.T) {
	_, repo := withStateRoot(t)
	original := runLoopState{
		Enabled:             true,
		SessionID:           "ses_test",
		ActiveRunID:         "ses_test_active",
		NoProgressNudges:    2,
		DisabledReason:      "",
		StopAnchorMessageID: "msg_anchor_123",
	}
	if err := saveRunLoopState(repo, original); err != nil {
		t.Fatalf("saveRunLoopState: %v", err)
	}
	loaded, err := loadRunLoopState(repo)
	if err != nil {
		t.Fatalf("loadRunLoopState: %v", err)
	}
	if !loaded.Enabled {
		t.Fatal("expected enabled")
	}
	if loaded.SessionID != "ses_test" {
		t.Fatalf("SessionID = %q", loaded.SessionID)
	}
	if loaded.ActiveRunID != "ses_test_active" {
		t.Fatalf("ActiveRunID = %q", loaded.ActiveRunID)
	}
	if loaded.NoProgressNudges != 2 {
		t.Fatalf("NoProgressNudges = %d", loaded.NoProgressNudges)
	}
	if loaded.StopAnchorMessageID != "msg_anchor_123" {
		t.Fatalf("StopAnchorMessageID = %q", loaded.StopAnchorMessageID)
	}
}

func TestReconcileRunLoopSessionEndResetsAll(t *testing.T) {
	_, repo := withStateRoot(t)
	if err := reconcileRunLoopState(repo, "ses_x", &HookPayload{SessionID: "ses_x"}, runLoopSessionStart); err != nil {
		t.Fatalf("session start: %v", err)
	}
	if err := reconcileRunLoopState(repo, "ses_x",
		&HookPayload{SessionID: "ses_x", Prompt: "/runloop"},
		runLoopUserPrompt,
	); err != nil {
		t.Fatalf("user prompt: %v", err)
	}
	before, _ := loadRunLoopState(repo)
	if !before.Enabled {
		t.Fatal("expected enabled before session end")
	}
	if err := reconcileRunLoopState(repo, "ses_x", &HookPayload{SessionID: "ses_x"}, runLoopSessionEnd); err != nil {
		t.Fatalf("session end: %v", err)
	}
	after, err := loadRunLoopState(repo)
	if err != nil {
		t.Fatal(err)
	}
	if after.Enabled {
		t.Fatal("expected disabled after session end")
	}
	if after.ActiveRunID != "" {
		t.Fatalf("expected empty active_run_id, got %q", after.ActiveRunID)
	}
	if after.NoProgressNudges != 0 {
		t.Fatalf("expected zero nudges, got %d", after.NoProgressNudges)
	}
	if after.DisabledReason != "" {
		t.Fatalf("expected empty disabled_reason, got %q", after.DisabledReason)
	}
	if after.SessionID != "ses_x" {
		t.Fatalf("expected session_id preserved, got %q", after.SessionID)
	}
}

func TestReconcileRunLoopSessionSwitchPreservesDisabled(t *testing.T) {
	_, repo := withStateRoot(t)
	if err := reconcileRunLoopState(repo, "old-session", &HookPayload{SessionID: "old-session"}, runLoopSessionStart); err != nil {
		t.Fatalf("session start: %v", err)
	}
	if err := reconcileRunLoopState(repo, "new-session", &HookPayload{SessionID: "new-session"}, runLoopSessionStart); err != nil {
		t.Fatalf("second session start: %v", err)
	}
	state, err := loadRunLoopState(repo)
	if err != nil {
		t.Fatal(err)
	}
	if state.Enabled {
		t.Fatal("expected disabled after fresh session start without runloop intent")
	}
	if state.SessionID != "new-session" {
		t.Fatalf("expected session_id updated, got %q", state.SessionID)
	}
}

func TestReconcileRunLoopSessionStartPreservesActiveRun(t *testing.T) {
	_, repo := withStateRoot(t)
	if err := reconcileRunLoopState(repo, "ses_race",
		&HookPayload{SessionID: "ses_race", Prompt: "/runloop"},
		runLoopUserPrompt,
	); err != nil {
		t.Fatalf("user prompt: %v", err)
	}
	if err := reconcileRunLoopState(repo, "ses_race",
		&HookPayload{SessionID: "ses_race"},
		runLoopSessionStart,
	); err != nil {
		t.Fatalf("session start: %v", err)
	}
	state, err := loadRunLoopState(repo)
	if err != nil {
		t.Fatal(err)
	}
	if !state.Enabled {
		t.Fatalf("expected SessionStart to preserve active runloop run, got %+v", state)
	}
	if state.ActiveRunID != "ses_race" {
		t.Fatalf("expected active run to survive SessionStart, got %+v", state)
	}
}

func TestRunLoopIntentFromUserPromptNoop(t *testing.T) {
	tests := []struct {
		name    string
		payload *HookPayload
	}{
		{name: "nil", payload: nil},
		{name: "empty", payload: &HookPayload{}},
		{name: "tool command ignored", payload: &HookPayload{SessionID: "s", ToolName: "Bash", ToolInput: map[string]interface{}{"command": "runloop"}}},
		{name: "bare old token ignored", payload: &HookPayload{SessionID: "s", Prompt: "runloop"}},
		{name: "error text ignored", payload: &HookPayload{SessionID: "s", Error: "user typed runloop"}},
		{name: "question ignored", payload: &HookPayload{SessionID: "s", Prompt: "was ist runloop?"}},
		{name: "runLoop mode with space ignored", payload: &HookPayload{SessionID: "s", Prompt: "runLoop mode"}},
		{name: "uppercase ignored", payload: &HookPayload{SessionID: "s", Prompt: "Runloop"}},
		{name: "with go command ignored", payload: &HookPayload{SessionID: "s", Prompt: "runloop go"}},
		{name: "raw please prompt ignored", payload: &HookPayload{SessionID: "s", Raw: map[string]interface{}{"prompt": "please runloop"}}},
		{name: "slash command glued to word ignored", payload: &HookPayload{SessionID: "s", Prompt: "/runloopgo"}},
		{name: "quoted slash command ignored", payload: &HookPayload{SessionID: "s", Prompt: "> /runloop\nnur zitiert"}},
		{name: "incidental short mention ignored", payload: &HookPayload{SessionID: "s", Prompt: "runloop verliert"}},
		{name: "incidental sentence ignored", payload: &HookPayload{SessionID: "s", Prompt: "ich schreibe runloop nur als wort"}},
		{name: "diagnostic noun ignored", payload: &HookPayload{SessionID: "s", Prompt: "runloop activation"}},
		{name: "autocontinue wording ignored", payload: &HookPayload{SessionID: "s", Prompt: "autocontinue runloop"}},
		{name: "long diagnostic mention ignored", payload: &HookPayload{SessionID: "s", Prompt: "da war der Stop hook block drin und der Text sagte runloop autocontinue, aber das ist nur Diagnose und soll nicht aktivieren"}},
		{name: "hook prompt block ignored", payload: &HookPayload{SessionID: "s", Prompt: `<hook_prompt hook_run_id="stop:5:/repo/.codex/hooks.json">runloop autocontinue

Continue the repository task lifecycle without asking for routine permission.
</hook_prompt>`}},
		{name: "hook prompt slash command ignored", payload: &HookPayload{SessionID: "s", Prompt: `<hook_prompt hook_run_id="stop:5:/repo/.codex/hooks.json">runloop autocontinue

/runloop autocontinue. Continue the repository task lifecycle without asking for routine permission.
</hook_prompt>`}},
		{name: "markdown quote ignored", payload: &HookPayload{SessionID: "s", Prompt: "> runloop\nnur zitiert"}},
		{name: "markdown quoted slash command ignored", payload: &HookPayload{SessionID: "s", Prompt: "> /runloop\nnur zitiert"}},
		{name: "fenced code ignored", payload: &HookPayload{SessionID: "s", Prompt: "```text\nrunloop\n```"}},
		{name: "fenced slash command ignored", payload: &HookPayload{SessionID: "s", Prompt: "```text\n/runloop\n```"}},
		{name: "inline quoted slash command ignored", payload: &HookPayload{SessionID: "s", Prompt: `ich zitiere "/runloop" nur als Text`}},
		{name: "multiline pasted chat quote ignored", payload: &HookPayload{SessionID: "s", Prompt: `hier ist der gesamte chat:

"Kurzantwort:
Du schickst einmal /runloop -> State enabled.
Cursor soll dann weiterlaufen."

analysiere warum das passiert ist`}},
		{name: "pasted assistant transcript bullet ignored", payload: &HookPayload{SessionID: "s", Prompt: `• Cursor hat /runloop im Prompt bekommen.
  Das ist nur ein zitierter Befund.`}},
		{name: "pasted user transcript marker ignored", payload: &HookPayload{SessionID: "s", Prompt: `› /runloop
  aus altem Chat kopiert`}},
		{name: "pasted opus no runloop diagnostic ignored", payload: &HookPayload{SessionID: "s", Prompt: `gerade opus onboarded und das hi ngegeben er sagt das:

Wo ich vor dem Go Klärung brauche
2. Runloop ist AUS (deine aktuelle Nachricht enthält kein /runloop). Die durchgehende Mechanik ist nur bei aktivem Runloop.

was sollten wir anpassen?`}},
		{name: "suggested append slash command ignored", payload: &HookPayload{SessionID: "s", Prompt: "Wenn du Opus im Runloop willst, häng dort /runloop dran."}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := runLoopIntentFromUserPrompt(tc.payload); got != runLoopIntentNoop {
				t.Fatalf("expected noop, got %v", got)
			}
		})
	}
}

func TestRunLoopIntentFromUserPromptEnable(t *testing.T) {
	tests := []struct {
		name    string
		payload *HookPayload
	}{
		{name: "exact slash command", payload: &HookPayload{SessionID: "s", Prompt: "/runloop"}},
		{name: "exact slash command with surrounding blank lines", payload: &HookPayload{SessionID: "s", Prompt: "\n  /runloop  \n"}},
		{name: "slash command plus text", payload: &HookPayload{SessionID: "s", Prompt: "/runloop mach weiter"}},
		{name: "slash command inside sentence", payload: &HookPayload{SessionID: "s", Prompt: "mach das onboard fertig /runloop und arbeite autonom"}},
		{name: "slash command on own line with instructions", payload: &HookPayload{SessionID: "s", Prompt: "bitte starten\n/runloop\nmach weiter"}},
		{name: "slash command with punctuation", payload: &HookPayload{SessionID: "s", Prompt: "los /runloop, bitte"}},
		{name: "hook prompt ignored but real slash command enables", payload: &HookPayload{SessionID: "s", Prompt: "<hook_prompt>runloop</hook_prompt>\n/runloop\nextra"}},
		{name: "quoted slash ignored but real slash command enables", payload: &HookPayload{SessionID: "s", Prompt: `"alte Zeile /runloop"
jetzt wirklich /runloop und arbeite weiter`}},
		{name: "diagnostic slash ignored but real slash command enables", payload: &HookPayload{SessionID: "s", Prompt: `Opus sagte: Runloop ist AUS (deine aktuelle Nachricht enthält kein /runloop).
jetzt wirklich /runloop und arbeite weiter`}},
		{name: "raw exact slash command", payload: &HookPayload{SessionID: "s", Raw: map[string]interface{}{"prompt": "/runloop"}}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := runLoopIntentFromUserPrompt(tc.payload); got != runLoopIntentEnable {
				t.Fatalf("expected enable, got %v", got)
			}
		})
	}
}

func TestRunLoopIntentFromUserPromptOffTextIsNoop(t *testing.T) {
	tests := []struct {
		name    string
		payload *HookPayload
	}{
		{name: "runLoop mode off", payload: &HookPayload{SessionID: "s", Prompt: "runLoop mode off"}},
		{name: "off runloop", payload: &HookPayload{SessionID: "s", Prompt: "turn off runloop please"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := runLoopIntentFromUserPrompt(tc.payload); got != runLoopIntentNoop {
				t.Fatalf("expected noop, got %v", got)
			}
		})
	}
}

func TestExtractRunLoopUserPromptTextNil(t *testing.T) {
	if got := extractRunLoopUserPromptText(nil); got != "" {
		t.Fatalf("expected empty string for nil payload, got %q", got)
	}
}

func TestReconcileRunLoopToolEventOffTextDoesNotDisable(t *testing.T) {
	_, repo := withStateRoot(t)
	if err := reconcileRunLoopState(repo, "s", &HookPayload{SessionID: "s", Prompt: "/runloop"}, runLoopUserPrompt); err != nil {
		t.Fatalf("enable: %v", err)
	}
	enabled, _ := loadRunLoopState(repo)
	if !enabled.Enabled {
		t.Fatal("expected enabled after enable tool event")
	}
	if err := reconcileRunLoopState(repo, "s", &HookPayload{SessionID: "s", ToolName: "Bash", ToolInput: map[string]interface{}{"command": "runLoop mode off"}}, runLoopToolEvent); err != nil {
		t.Fatalf("off text event: %v", err)
	}
	disabled, _ := loadRunLoopState(repo)
	if !disabled.Enabled {
		t.Fatal("expected runloop to stay enabled")
	}
	if disabled.DisabledReason != "" {
		t.Fatalf("unexpected disabled_reason=%q", disabled.DisabledReason)
	}
}

func TestReconcileRunLoopToolEventTextDoesNotEnable(t *testing.T) {
	_, repo := withStateRoot(t)
	if err := reconcileRunLoopState(repo, "s", &HookPayload{SessionID: "s", ToolName: "Bash", ToolInput: map[string]interface{}{"command": "runloop"}}, runLoopToolEvent); err != nil {
		t.Fatalf("tool event: %v", err)
	}
	state, err := loadRunLoopState(repo)
	if err != nil {
		t.Fatal(err)
	}
	if state.Enabled {
		t.Fatalf("tool text must not enable runloop, got %+v", state)
	}
}

func TestRunLoopStopFileOverridesEnabledState(t *testing.T) {
	_, repo := withStateRoot(t)
	if err := saveRunLoopState(repo, runLoopState{
		Enabled:     true,
		SessionID:   "s",
		ActiveRunID: "s",
	}); err != nil {
		t.Fatal(err)
	}
	if err := writeRunLoopStopFile(repo, "s", "s", "test"); err != nil {
		t.Fatal(err)
	}
	state, err := loadRunLoopState(repo)
	if err != nil {
		t.Fatal(err)
	}
	if state.Enabled || state.DisabledReason != "stop_file" {
		t.Fatalf("stopfile must logically disable state, got %+v", state)
	}
	if err := reconcileRunLoopState(repo, "s", &HookPayload{SessionID: "s", Prompt: "/runloop"}, runLoopUserPrompt); err != nil {
		t.Fatalf("explicit prompt re-enable: %v", err)
	}
	state, err = loadRunLoopState(repo)
	if err != nil {
		t.Fatal(err)
	}
	if !state.Enabled || state.DisabledReason != "" {
		t.Fatalf("explicit /runloop prompt must clear stopfile and enable, got %+v", state)
	}
	if hasRunLoopStopFile(repo) {
		t.Fatal("expected explicit /runloop prompt to clear stopfile")
	}
}

func TestRunLoopStopFileOverridesSessionStart(t *testing.T) {
	_, repo := withStateRoot(t)
	if err := reconcileRunLoopState(repo, "s", &HookPayload{SessionID: "s", Prompt: "/runloop"}, runLoopUserPrompt); err != nil {
		t.Fatalf("enable: %v", err)
	}
	if err := writeRunLoopStopFile(repo, "s", "s", "test"); err != nil {
		t.Fatal(err)
	}
	if err := reconcileRunLoopState(repo, "s", &HookPayload{SessionID: "s"}, runLoopSessionStart); err != nil {
		t.Fatalf("session start: %v", err)
	}
	state, err := loadRunLoopState(repo)
	if err != nil {
		t.Fatal(err)
	}
	if state.Enabled || state.DisabledReason != "stop_file" {
		t.Fatalf("stopfile must beat SessionStart preservation, got %+v", state)
	}
}

func TestReconcileRunLoopNormalPromptFromOtherRuntimeDoesNotStopActiveRun(t *testing.T) {
	_, repo := withStateRoot(t)
	if err := reconcileRunLoopStateForRuntime(repo, "shared-session", "cursor", &HookPayload{SessionID: "shared-session", Prompt: "/runloop"}, runLoopUserPrompt); err != nil {
		t.Fatalf("enable cursor run: %v", err)
	}
	if err := reconcileRunLoopStateForRuntime(repo, "shared-session", "codex", &HookPayload{SessionID: "shared-session", Prompt: "ist der runloop an?"}, runLoopUserPrompt); err != nil {
		t.Fatalf("codex control prompt: %v", err)
	}
	state, err := loadRunLoopState(repo)
	if err != nil {
		t.Fatal(err)
	}
	if !state.Enabled || state.Runtime != "cursor" || state.ActiveRunID != "shared-session" {
		t.Fatalf("other runtime prompt must preserve cursor runloop run, got %+v", state)
	}
	if hasRunLoopStopFile(repo) {
		t.Fatal("other runtime prompt must not write a stopfile for the active cursor run")
	}
}

func TestReconcileRunLoopNormalPromptFromSameRuntimeStopsActiveRun(t *testing.T) {
	_, repo := withStateRoot(t)
	if err := reconcileRunLoopStateForRuntime(repo, "cursor-session", "cursor", &HookPayload{SessionID: "cursor-session", Prompt: "/runloop"}, runLoopUserPrompt); err != nil {
		t.Fatalf("enable cursor run: %v", err)
	}
	if err := reconcileRunLoopStateForRuntime(repo, "cursor-session", "cursor", &HookPayload{SessionID: "cursor-session", Prompt: "normal prompt"}, runLoopUserPrompt); err != nil {
		t.Fatalf("same runtime prompt: %v", err)
	}
	state, err := loadRunLoopState(repo)
	if err != nil {
		t.Fatal(err)
	}
	if state.Enabled || state.DisabledReason != "user_prompt" {
		t.Fatalf("same runtime normal prompt must disable active run, got %+v", state)
	}
	marker, exists, legacy := readRunLoopStopMarker(repo)
	if !exists || legacy {
		t.Fatalf("expected structured same-runtime stop marker, exists=%v legacy=%v", exists, legacy)
	}
	if marker.Runtime != "cursor" || marker.Reason != "user_prompt" {
		t.Fatalf("expected cursor user_prompt stop marker, got %+v", marker)
	}
}

func TestIsUserStopInterruptRejectsCompaction(t *testing.T) {
	yes := true
	tests := []struct {
		name    string
		payload *HookPayload
		want    bool
	}{
		{name: "compaction error", payload: &HookPayload{Error: "session compaction completed"}, want: false},
		{name: "compaction in raw", payload: &HookPayload{Raw: map[string]interface{}{"type": "session.compaction"}}, want: false},
		{name: "timeout error", payload: &HookPayload{Error: "session timeout exceeded"}, want: false},
		{name: "real interrupt", payload: &HookPayload{IsInterrupt: &yes}, want: true},
		{name: "real abort", payload: &HookPayload{IsInterrupt: &yes}, want: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := isUserStopInterrupt(tc.payload); got != tc.want {
				t.Fatalf("isUserStopInterrupt() = %v, want %v", got, tc.want)
			}
		})
	}
}
