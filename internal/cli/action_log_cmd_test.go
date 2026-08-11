package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"reconc.dev/reconc/internal/action"
	"reconc.dev/reconc/internal/actionledger"
	"reconc.dev/reconc/internal/actionstate"
	"reconc.dev/reconc/internal/compiler"
	"reconc.dev/reconc/internal/retention"
	productruntime "reconc.dev/reconc/internal/runtime"
	productagentsession "reconc.dev/reconc/internal/runtime/agentsession"
)

func TestActionLogCommandsTreatAbsentLedgerAsEmptyWithoutCreatingState(t *testing.T) {
	home := filepath.Join(t.TempDir(), "absent-home")
	repository := t.TempDir()
	t.Setenv("RECONC_HOME", home)
	tests := [][]string{
		{"action", "log", "tail", repository, "--json"},
		{"action", "log", "stats", repository, "--json"},
		{"action", "log", "verify", repository, "--json"},
		{"action", "log", "export", repository},
	}
	for _, arguments := range tests {
		var stdout, stderr bytes.Buffer
		if err := Run(arguments, "0.9.6", &stdout, &stderr); err != nil {
			t.Fatalf("Run(%v): %v", arguments, err)
		}
		var object map[string]any
		if err := json.Unmarshal(stdout.Bytes(), &object); err != nil || object["format_version"] == "" {
			t.Fatalf("Run(%v) output = %q, %v", arguments, stdout.Bytes(), err)
		}
	}
	if _, err := os.Lstat(home); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("read-only action log commands created state: %v", err)
	}
}

func TestActionLogCommandsTreatMissingLedgerInExistingActionStateAsEmpty(t *testing.T) {
	home := filepath.Join(t.TempDir(), "existing-home")
	repository := t.TempDir()
	t.Setenv("RECONC_HOME", home)
	if _, err := actionstate.CreateIdentityKey(home, time.Date(2026, 8, 11, 12, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	lease, err := actionstate.AcquireIdentityKey(context.Background(), home)
	if err != nil {
		t.Fatal(err)
	}
	state, err := actionstate.OpenStore(actionstate.StoreOptions{
		Home: home, Repository: repository, KeyLease: lease, OwnerID: "empty-ledger-cli-test",
	})
	if err != nil {
		t.Fatal(err)
	}
	storage, err := state.PrivateProjectStorage()
	if err != nil {
		t.Fatal(err)
	}
	ledgerLock := filepath.Join(storage.ActionDirectory(), "ledger.lock")
	if err := lease.Close(); err != nil {
		t.Fatal(err)
	}
	commands := [][]string{
		{"action", "log", "tail", repository, "--json"},
		{"action", "log", "stats", repository, "--json"},
		{"action", "log", "verify", repository, "--json"},
		{"action", "log", "export", repository},
	}
	for _, arguments := range commands {
		var stdout bytes.Buffer
		if err := Run(arguments, "0.9.6", &stdout, &bytes.Buffer{}); err != nil {
			t.Fatalf("Run(%v): %v", arguments, err)
		}
	}
	if _, err := os.Lstat(ledgerLock); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("read-only action log commands created a missing ledger lock: %v", err)
	}
}

func TestActionLogCommandsVerifyFilterAggregateAndExportRetainedCall(t *testing.T) {
	repository, home, callID := createActionLogFixture(t)
	t.Setenv("RECONC_HOME", home)
	commands := []struct {
		arguments []string
		contains  []string
	}{
		{arguments: []string{"action", "log", "tail", repository, "--call", callID, "-n", "1", "--json"}, contains: []string{`"event": "pre_decision"`, callID}},
		{arguments: []string{"action", "log", "stats", repository, "--decision", "block", "--json"}, contains: []string{`"calls": 1`, `"blocked": 1`}},
		{arguments: []string{"action", "log", "verify", repository, "--json"}, contains: []string{`"integrity": "verified"`, `"calls_complete": true`}},
		{arguments: []string{"action", "log", "export", repository, "--call", callID}, contains: []string{`"exported_cases": 1`, `"ledger"`, `"replay_complete": false`}},
	}
	for _, command := range commands {
		var stdout, stderr bytes.Buffer
		if err := Run(command.arguments, "0.9.6", &stdout, &stderr); err != nil {
			t.Fatalf("Run(%v): %v\nstderr=%s", command.arguments, err, stderr.String())
		}
		for _, want := range command.contains {
			if !strings.Contains(stdout.String(), want) {
				t.Fatalf("Run(%v) output lacks %q:\n%s", command.arguments, want, stdout.String())
			}
		}
	}
}

func TestActionLogVerifyEmitsInvalidReportBeforeReturningFailure(t *testing.T) {
	repository, home, _ := createActionLogFixture(t)
	t.Setenv("RECONC_HOME", home)
	resolvedRepository, err := productagentsession.ResolveRepoRoot(repository)
	if err != nil {
		t.Fatal(err)
	}
	head := filepath.Join(retention.ProjectDir(home, resolvedRepository), "action", "ledger.head.json")
	if err := os.Remove(head); err != nil {
		t.Fatal(err)
	}
	var stdout bytes.Buffer
	err = Run([]string{"action", "log", "verify", repository, "--json"}, "0.9.6", &stdout, &bytes.Buffer{})
	if err == nil || !strings.Contains(stdout.String(), `"integrity": "invalid"`) ||
		!strings.Contains(stdout.String(), `"detached_head": "absent"`) ||
		!strings.Contains(stdout.String(), `"events_evaluated": true`) ||
		!strings.Contains(stdout.String(), `"calls_evaluated": true`) {
		t.Fatalf("invalid verification output = %q, error = %v", stdout.String(), err)
	}
}

func TestActionLogExportRefusesExistingOutputWithoutChangingIt(t *testing.T) {
	repository, home, _ := createActionLogFixture(t)
	t.Setenv("RECONC_HOME", home)
	output := filepath.Join(t.TempDir(), "ledger-export.json")
	var first bytes.Buffer
	if err := Run([]string{"action", "log", "export", repository, "--output", output}, "0.9.6", &first, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	written, err := os.ReadFile(output)
	if err != nil || !bytes.Equal(written, first.Bytes()) {
		t.Fatalf("output bytes differ: %v", err)
	}
	if runtime.GOOS != "windows" {
		info, statErr := os.Stat(output)
		if statErr != nil || info.Mode().Perm() != 0o600 {
			t.Fatalf("output mode = %v, %v", info, statErr)
		}
	}
	var second bytes.Buffer
	err = Run([]string{"action", "log", "export", repository, "--output", output}, "0.9.6", &second, &bytes.Buffer{})
	if err == nil || second.Len() != 0 {
		t.Fatalf("existing output was accepted: output=%q err=%v", second.String(), err)
	}
	after, readErr := os.ReadFile(output)
	if readErr != nil || !bytes.Equal(after, written) {
		t.Fatalf("existing output changed: %v", readErr)
	}
}

func TestActionLogOptionValidationRejectsAmbiguousOrUnsafeFilters(t *testing.T) {
	repository := t.TempDir()
	tests := [][]string{
		{"action", "log", "tail", repository, "-n", "0"},
		{"action", "log", "tail", repository, "--call", "bad"},
		{"action", "log", "stats", repository, "--event", "invented"},
		{"action", "log", "verify", repository, "--decision", "block"},
		{"action", "log", "export", repository, "--output"},
		{"action", "log", "export", repository, "--json"},
		{"action", "log", "tail", repository, "--json", "--json"},
	}
	for _, arguments := range tests {
		if err := Run(arguments, "0.9.6", &bytes.Buffer{}, &bytes.Buffer{}); err == nil {
			t.Fatalf("Run(%v) accepted invalid options", arguments)
		}
	}
}

func createActionLogFixture(t *testing.T) (string, string, string) {
	t.Helper()
	repository := t.TempDir()
	home := filepath.Join(t.TempDir(), "reconc-home")
	if _, err := actionstate.CreateIdentityKey(home, time.Date(2026, 8, 11, 12, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	lease, err := actionstate.AcquireIdentityKey(context.Background(), home)
	if err != nil {
		t.Fatal(err)
	}
	serverIdentity := lease.Key.Identity(actionstate.DomainLedger, []byte("server"))
	policy := `default_mode: warn
actions:
  tools:
    - id: database-write
      transport: mcp_stdio
      server_label: warehouse
      server_fingerprint: ` + serverIdentity + `
      tool: execute
      effect:
        kind: external
      ledger_name_safe: true
  ledger:
    tool_identity: exact_name
  rules:
    - id: block-production
      selector:
        tool_ids: [database-write]
      decision: block
rules: []
`
	if err := os.WriteFile(filepath.Join(repository, ".reconc.yml"), []byte(policy), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := compiler.CompileRepoPolicy(repository, "action-log-test"); err != nil {
		t.Fatal(err)
	}
	compiledPolicy, _, err := productruntime.NewEvaluator().CurrentCompiledPolicyEvaluator(repository)
	if err != nil {
		t.Fatal(err)
	}
	compiled, err := compiledPolicy.ActionRuntime()
	if err != nil {
		t.Fatal(err)
	}
	stateStore, err := actionstate.OpenStore(actionstate.StoreOptions{
		Home: home, Repository: repository, KeyLease: lease, OwnerID: "action-log-cli-test",
	})
	if err != nil {
		t.Fatal(err)
	}
	storage, err := stateStore.PrivateProjectStorage()
	if err != nil {
		t.Fatal(err)
	}
	ledger, err := actionledger.OpenStore(storage)
	if err != nil {
		t.Fatal(err)
	}
	callID := "act_aaaaaaaaaaaaaaaaaaaaaaaaaa"
	binding := actionledger.CallBinding{
		CallID: callID, RequestIdentity: lease.Key.Identity(actionstate.DomainLedger, []byte("request")),
		RepositoryIdentity: storage.RepositoryIdentity(), PolicyDigest: compiled.SourceDigest,
		LockDigest: compiled.LockDigest, ServerLabel: "warehouse", ServerFingerprint: serverIdentity,
		Tool:               actionledger.ToolIdentity{Mode: action.LedgerExactName, Value: "execute"},
		ToolContractDigest: "sha256:" + strings.Repeat("3", 64), Principal: "operator",
		CredentialLabels:  []string{"database-writer"},
		RunIdentity:       lease.Key.Identity(actionstate.DomainLedger, []byte("run")),
		SessionIdentity:   lease.Key.Identity(actionstate.DomainLedger, []byte("session")),
		ContextIdentity:   lease.Key.Identity(actionstate.DomainLedger, []byte("context")),
		ContextProvenance: action.ProvenanceOperatorBound,
	}
	completeness := action.CompleteEvidence()
	records := []actionledger.Record{
		{
			Timestamp: "2026-08-11T12:00:00Z", Event: actionledger.EventRequestAccepted, Call: binding,
			Decision:        actionledger.DecisionBinding{Phase: action.PhasePreCall, RuleIDs: []string{}, Completeness: completeness},
			SelectedFields:  []actionledger.SelectedFieldEvidence{},
			RequestAccepted: &actionledger.RequestAccepted{ArgumentBytes: 2},
		},
		{
			Timestamp: "2026-08-11T12:00:00.000001Z", Event: actionledger.EventPreDecision, Call: binding,
			Decision: actionledger.DecisionBinding{
				Phase: action.PhasePreCall, Decision: action.DecisionBlock, Reason: action.ReasonRuleMatched,
				RuleIDs: []string{"block-production"}, Completeness: completeness,
			},
			SelectedFields: []actionledger.SelectedFieldEvidence{},
			PreDecision:    &actionledger.PreDecision{Outcome: action.OutcomeDispatchBlocked},
		},
	}
	for _, record := range records {
		if _, err := ledger.Append(context.Background(), record); err != nil {
			t.Fatal(err)
		}
	}
	if err := lease.Close(); err != nil {
		t.Fatal(err)
	}
	return repository, home, callID
}
