package agentsession

import (
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"

	"reconc.dev/reconc/internal/runtime"
)

func TestRecordStopBlockAndRepeatedPersistsFeedbackOnlyAfterMutation(t *testing.T) {
	repo := setupPolicyRepo(t)
	if _, err := InitializeSessionState(repo, "repeat-state"); err != nil {
		t.Fatal(err)
	}
	root, err := ResolveRepoRoot(repo)
	if err != nil {
		t.Fatal(err)
	}
	violations := []runtime.Violation{{RuleID: "stop-gate", RecommendedAction: "run the gate"}}

	first := recordStopBlockAndRepeated(root, "repeat-state", violations)
	if first.err != nil || first.repeated || first.feedbackID == "" {
		t.Fatalf("first stop record = %+v, want durable non-repeated feedback", first)
	}
	state, err := LoadSessionState(repo, "repeat-state")
	if err != nil {
		t.Fatal(err)
	}
	if state.LastStopBlockViolationHash == "" {
		t.Fatal("first stop did not persist the blocking violation hash")
	}

	repeated := recordStopBlockAndRepeated(root, "repeat-state", violations)
	if repeated.err != nil || !repeated.repeated || repeated.feedbackID != first.feedbackID {
		t.Fatalf("repeated stop record = %+v, want the same durable feedback and repeated=true", repeated)
	}
}

func TestStopBlockJSONOutputDoesNotClaimUnconfirmedState(t *testing.T) {
	repo := setupPolicyRepo(t)
	if _, err := InitializeSessionState(repo, "corrupt-repeat-state"); err != nil {
		t.Fatal(err)
	}
	root, err := ResolveRepoRoot(repo)
	if err != nil {
		t.Fatal(err)
	}
	statePath := sessionStatePath(root, "corrupt-repeat-state")
	corrupt := []byte("{not-json\n")
	if err := os.WriteFile(statePath, corrupt, 0o600); err != nil {
		t.Fatal(err)
	}
	violations := []runtime.Violation{{RuleID: "stop-gate", RecommendedAction: "run the gate"}}

	output, recordErr := stopBlockJSONOutput(root, "corrupt-repeat-state", nil, violations)
	if recordErr == nil {
		t.Fatal("corrupt session state did not surface a repeat-tracking error")
	}
	if strings.Contains(output, "Feedback:") {
		t.Fatalf("failed repeat-state mutation claimed feedback durability: %s", output)
	}
	var response struct {
		Decision string `json:"decision"`
		Reason   string `json:"reason"`
	}
	if err := json.Unmarshal([]byte(output), &response); err != nil {
		t.Fatalf("invalid stop block JSON: %v", err)
	}
	if response.Decision != "block" || !strings.Contains(response.Reason, "durability is unconfirmed") {
		t.Fatalf("stop response = %+v, want a valid fail-closed warning", response)
	}
	if got, err := os.ReadFile(statePath); err != nil || string(got) != string(corrupt) {
		t.Fatalf("failed repeat-state mutation changed corrupt state: %q (%v)", got, err)
	}

	diagnostic := stopBlockStateDiagnostic(errors.New(strings.Repeat("x", 10000)))
	if !strings.HasPrefix(diagnostic, "reconc stop state (warn): ") || len(diagnostic) > len("reconc stop state (warn): ")+4096 {
		t.Fatalf("repeat-state diagnostic is not bounded: %d bytes", len(diagnostic))
	}
	adapted := AdaptCursorResult("cursor-stop", Result{Stdout: output, Stderr: diagnostic})
	var cursorResponse map[string]interface{}
	if err := json.Unmarshal([]byte(adapted.Stdout), &cursorResponse); err != nil {
		t.Fatalf("Cursor adapter emitted invalid JSON: %v", err)
	}
	if _, ok := cursorResponse["followup_message"].(string); !ok {
		t.Fatalf("Cursor adapter dropped the fail-closed block response: %s", adapted.Stdout)
	}
}
