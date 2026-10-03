package completiongate_test

import (
	"testing"

	"reconc.dev/reconc/internal/completiongate"
	"reconc.dev/reconc/internal/runtime"
	"reconc.dev/reconc/internal/runtime/agentsession"
)

func TestCompletionUsesStartBoundNativeAssuranceEvidence(t *testing.T) {
	t.Setenv(agentsession.StateRootEnv, t.TempDir())
	repo := completionRepo(t, `rules:
  - id: native
    kind: require_assurance
    mode: block
    when_paths: ["**/*.go"]
    message: current verification required
    assurance:
      - id: live
        type: live_verification
        commands: ["go test ./..."]
`, map[string]string{"main.go": "package main\n"})
	if _, err := agentsession.InitializeSessionState(repo, "causal"); err != nil {
		t.Fatal(err)
	}
	for _, want := range []runtime.Decision{runtime.DecisionBlock, runtime.DecisionPass} {
		if _, err := agentsession.MutateSessionState(repo, "causal", func(state agentsession.SessionState) agentsession.SessionState {
			return agentsession.RecordWriteEvent(state, []string{"main.go"})
		}); err != nil {
			t.Fatal(err)
		}
		binding, err := agentsession.CaptureCommandExecution(repo)
		if err != nil {
			t.Fatal(err)
		}
		if want == runtime.DecisionBlock {
			if _, err := agentsession.MutateSessionState(repo, "causal", func(state agentsession.SessionState) agentsession.SessionState {
				return agentsession.RecordWriteEvent(state, []string{"main.go"})
			}); err != nil {
				t.Fatal(err)
			}
		}
		if err := agentsession.RecordCommandOutcome(binding, "rtk go test ./...", "success", 0); err != nil {
			t.Fatal(err)
		}
		report := evaluateCompletion(t, repo, completiongate.Options{})
		if report.PolicyReport == nil || report.PolicyReport.Decision != want {
			t.Fatalf("completion policy=%+v, want %s", report.PolicyReport, want)
		}
	}
}
