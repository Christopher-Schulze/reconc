package agentsession

import (
	"path/filepath"
	"testing"
)

func TestRecordCommandOutcomeAddsRealActiveSessionEvidence(t *testing.T) {
	t.Setenv(StateRootEnv, filepath.Join(t.TempDir(), "state"))
	repo := t.TempDir()
	if result := RunSessionStart(repo, []byte(`{"session_id":"session-1"}`)); result.ExitCode != 0 {
		t.Fatalf("session start: %s", result.Stderr)
	}
	writePayload := `{"session_id":"session-1","tool_name":"Write","tool_input":{"file_path":"src/main.go"}}`
	if result := RunPostToolUse(repo, []byte(writePayload)); result.ExitCode != 0 || result.Stderr != "" {
		t.Fatalf("write evidence: exit=%d stderr=%s", result.ExitCode, result.Stderr)
	}
	if err := RecordCommandOutcome(repo, "go test ./...", "success", 0); err != nil {
		t.Fatal(err)
	}
	evidence, err := ActiveEvidence(repo)
	if err != nil {
		t.Fatal(err)
	}
	if len(evidence.Commands) != 1 || evidence.Commands[0] != "go test ./..." {
		t.Fatalf("commands = %+v", evidence.Commands)
	}
	if len(evidence.CommandResults) != 1 || evidence.CommandResults[0].Command != "go test ./..." || evidence.CommandResults[0].Outcome != "success" || evidence.CommandResults[0].EvidenceEpoch == 0 || evidence.CommandResults[0].EvidenceEpoch != evidence.EvidenceEpoch || evidence.CommandResults[0].ToolUseID != "reconc-exec" || evidence.CommandResults[0].ExitCode == nil || *evidence.CommandResults[0].ExitCode != 0 {
		t.Fatalf("results = %+v", evidence.CommandResults)
	}
}

func TestRecordCommandOutcomeNeedsNoActiveSession(t *testing.T) {
	t.Setenv(StateRootEnv, filepath.Join(t.TempDir(), "state"))
	if err := RecordCommandOutcome(t.TempDir(), "go test ./...", "success", 0); err != nil {
		t.Fatal(err)
	}
}
