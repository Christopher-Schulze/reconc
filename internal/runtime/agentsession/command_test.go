package agentsession

import (
	"os"
	"path/filepath"
	"strings"
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

func TestActiveEvidenceRejectsCorruptActiveState(t *testing.T) {
	t.Setenv(StateRootEnv, filepath.Join(t.TempDir(), "state"))
	repo := t.TempDir()
	if _, err := InitializeSessionState(repo, "session-1"); err != nil {
		t.Fatal(err)
	}
	root, err := ResolveRepoRoot(repo)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(sessionStatePath(root, "session-1"), []byte("{invalid"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ActiveEvidence(repo); err == nil || !strings.Contains(err.Error(), "not valid JSON") {
		t.Fatalf("expected corrupt active-state error, got %v", err)
	}
}

func TestRecordClaimSurfacesReportRefreshFailure(t *testing.T) {
	t.Setenv(StateRootEnv, filepath.Join(t.TempDir(), "state"))
	repo := t.TempDir()
	if _, err := InitializeSessionState(repo, "session-1"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, ".reconc.yml"), []byte("rules: [\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := RecordClaim(repo, "ci-green", "session-1"); err == nil || !strings.Contains(err.Error(), "refresh report") {
		t.Fatalf("expected report-refresh error, got %v", err)
	}
	state, err := LoadSessionState(repo, "session-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(state.Claims) != 1 || state.Claims[0] != "ci-green" {
		t.Fatalf("idempotent claim mutation was not preserved: %+v", state.Claims)
	}
}
