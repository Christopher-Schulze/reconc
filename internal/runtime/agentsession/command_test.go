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
	binding, err := CaptureCommandExecution(repo)
	if err != nil {
		t.Fatal(err)
	}
	if err := RecordCommandOutcome(binding, "go test ./...", "success", 0); err != nil {
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
	binding, err := CaptureCommandExecution(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := RecordCommandOutcome(binding, "go test ./...", "success", 0); err != nil {
		t.Fatal(err)
	}
}

func TestCommandExecutionRejectsUnavailableOrTaintedBoundState(t *testing.T) {
	for _, name := range []string{"ended", "corrupt", "tainted"} {
		t.Run(name, func(t *testing.T) {
			t.Setenv(StateRootEnv, filepath.Join(t.TempDir(), "state"))
			repo := t.TempDir()
			if _, err := InitializeSessionState(repo, "original"); err != nil {
				t.Fatal(err)
			}
			binding, err := CaptureCommandExecution(repo)
			if err != nil {
				t.Fatal(err)
			}
			path := sessionStatePath(binding.root.Path(), "original")
			switch name {
			case "ended":
				if err := CleanupSessionState(repo, "original"); err != nil {
					t.Fatal(err)
				}
			case "corrupt":
				if err := os.WriteFile(path, []byte("{invalid"), 0o600); err != nil {
					t.Fatal(err)
				}
			case "tainted":
				if _, err := MutateSessionState(repo, "original", func(state SessionState) SessionState {
					state.EvidenceOverflow = true
					state.EvidenceOverflowReason = "commands"
					return state
				}); err != nil {
					t.Fatal(err)
				}
			}
			if err := RecordCommandOutcome(binding, "go version", "success", 0); err == nil {
				t.Fatal("unavailable or tainted execution owner accepted a result")
			}
			if name == "ended" {
				if _, err := os.Stat(path); !os.IsNotExist(err) {
					t.Fatalf("ended session was recreated: %v", err)
				}
			} else if name == "corrupt" {
				body, err := os.ReadFile(path)
				if err != nil || string(body) != "{invalid" {
					t.Fatalf("corrupt owner was overwritten: %q, %v", body, err)
				}
			} else {
				state, err := LoadSessionState(repo, "original")
				if err != nil || len(state.CommandResults) != 0 {
					t.Fatalf("tainted owner accepted command results: %+v, %v", state.CommandResults, err)
				}
			}
		})
	}
}

func TestCommandExecutionsShareOneSessionGeneration(t *testing.T) {
	t.Setenv(StateRootEnv, filepath.Join(t.TempDir(), "state"))
	repo := t.TempDir()
	if _, err := InitializeSessionState(repo, "original"); err != nil {
		t.Fatal(err)
	}
	bindings := make([]CommandExecution, 2)
	for index := range bindings {
		binding, err := CaptureCommandExecution(repo)
		if err != nil {
			t.Fatal(err)
		}
		bindings[index] = binding
	}
	if bindings[0].generation == "" || bindings[0].generation != bindings[1].generation {
		t.Fatal("overlapping executions did not share their session generation")
	}
	for _, binding := range bindings {
		if err := RecordCommandOutcome(binding, "go version", "success", 0); err != nil {
			t.Fatal(err)
		}
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
