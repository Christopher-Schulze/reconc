package agentsession

import (
	"testing"
	"time"
)

func TestRepoRunPolicyCheckpointTriggersAreBounded(t *testing.T) {
	now := time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC)
	run := repositoryRunState{Enabled: true, EnabledAt: now.UnixNano()}
	if repoRunPolicyCheckpointDue(run, SessionState{MaterialEvents: 1}, now.Add(time.Minute)) {
		t.Fatal("one recent material event must stay on the routine fast path")
	}
	if !repoRunPolicyCheckpointDue(run, SessionState{MaterialEvents: repositoryRunCheckpointEvents}, now.Add(time.Minute)) {
		t.Fatal("material-event budget must trigger a checkpoint")
	}
	failed := SessionState{MaterialEvents: 1, CommandResults: []CommandResult{{Command: "go test ./...", Outcome: "failure"}}}
	if !repoRunPolicyCheckpointDue(run, failed, now.Add(time.Minute)) {
		t.Fatal("failed command must trigger a risk checkpoint")
	}
	if !repoRunPolicyCheckpointDue(run, SessionState{MaterialEvents: 1}, now.Add(repositoryRunCheckpointInterval)) {
		t.Fatal("elapsed checkpoint interval with material progress must trigger")
	}
	run.CheckpointMaterial = 1
	if repoRunPolicyCheckpointDue(run, failed, now.Add(time.Hour)) {
		t.Fatal("unchanged material evidence must not repeat a checkpoint")
	}
}

func TestMaterialProgressDeduplicatesIdenticalToolOutcomes(t *testing.T) {
	payload := &HookPayload{ToolName: "Bash", ToolInput: map[string]interface{}{"command": "go test ./..."}}
	state := emptyState(t.TempDir(), "material")
	var err error
	state, err = recordToolUse(state, payload)
	if err != nil {
		t.Fatal(err)
	}
	state, err = recordToolUse(state, payload)
	if err != nil {
		t.Fatal(err)
	}
	if state.MaterialEvents != 1 {
		t.Fatalf("identical command outcomes must count once, got %d", state.MaterialEvents)
	}
	payload.ToolInput["command"] = "go vet ./..."
	state, err = recordToolUse(state, payload)
	if err != nil {
		t.Fatal(err)
	}
	if state.MaterialEvents != 2 {
		t.Fatalf("different command outcome must advance progress, got %d", state.MaterialEvents)
	}
}
