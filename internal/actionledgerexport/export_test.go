package actionledgerexport

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"reconc.dev/reconc/internal/action"
	"reconc.dev/reconc/internal/actionledger"
	"reconc.dev/reconc/internal/actionstate"
	"reconc.dev/reconc/internal/impactlab"
	"reconc.dev/reconc/internal/runtime"
)

type exportFixture struct {
	store    *actionledger.Store
	storage  actionstate.PrivateProjectStorage
	lease    *actionstate.IdentityKeyLease
	compiled runtime.CompiledActionRuntime
	repo     string
	nextTime time.Time
}

func newExportFixture(t *testing.T) *exportFixture {
	t.Helper()
	home := filepath.Join(t.TempDir(), "reconc-home")
	repository := t.TempDir()
	if _, err := actionstate.CreateIdentityKey(home, time.Date(2026, 8, 11, 12, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	lease, err := actionstate.AcquireIdentityKey(context.Background(), home)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := lease.Close(); err != nil {
			t.Errorf("close identity lease: %v", err)
		}
	})
	stateStore, err := actionstate.OpenStore(actionstate.StoreOptions{
		Home: home, Repository: repository, KeyLease: lease, OwnerID: "ledger-export-test",
	})
	if err != nil {
		t.Fatal(err)
	}
	storage, err := stateStore.PrivateProjectStorage()
	if err != nil {
		t.Fatal(err)
	}
	store, err := actionledger.OpenStore(storage)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := action.CompilePlan(action.Plan{
		Tools: []action.Tool{{
			ID: "database-write", Transport: action.TransportMCPStdio,
			ServerLabel: "warehouse", Tool: "execute",
			Effect:         action.Effect{Kind: action.EffectExternal},
			LedgerNameSafe: true, Origin: action.OriginActions, SourceIdentity: "policy",
		}},
		Rules: []action.Rule{{
			ID: "block-production", Selector: action.Selector{ToolIDs: []string{"database-write"}},
			Decision: action.DecisionBlock, SourceIdentity: "policy",
		}},
		Ledger: &action.LedgerPolicy{
			Mode: action.LedgerRequired, ToolIdentity: action.LedgerExactName,
			SelectedFields: []action.LedgerField{},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	evaluator, err := action.NewEvaluator(plan)
	if err != nil {
		t.Fatal(err)
	}
	return &exportFixture{
		store: store, storage: storage, lease: lease, repo: repository,
		compiled: runtime.CompiledActionRuntime{
			Evaluator: evaluator, SourceDigest: strings.Repeat("1", 64),
			LockDigest: strings.Repeat("2", 64), ToolCount: 1, ActionRuleCount: 1,
		},
		nextTime: time.Date(2026, 8, 11, 12, 0, 0, 0, time.UTC),
	}
}

func (f *exportFixture) identity(label string) string {
	return f.lease.Key.Identity(actionstate.DomainLedger, []byte(label))
}

func (f *exportFixture) appendBlockedCall(
	t *testing.T,
	callID string,
	selected bool,
	toolIdentity action.LedgerToolIdentity,
) {
	t.Helper()
	requestTime := f.nextTime
	decisionTime := requestTime.Add(time.Microsecond)
	f.nextTime = decisionTime.Add(time.Microsecond)
	toolValue := "execute"
	if toolIdentity == action.LedgerDeclarationID {
		toolValue = "database-write"
	}
	binding := actionledger.CallBinding{
		CallID: callID, RequestIdentity: f.identity("request-" + callID),
		RepositoryIdentity: f.storage.RepositoryIdentity(),
		PolicyDigest:       strings.Repeat("1", 64), LockDigest: strings.Repeat("2", 64),
		ServerLabel: "warehouse", ServerFingerprint: f.identity("server"),
		Tool:               actionledger.ToolIdentity{Mode: toolIdentity, Value: toolValue},
		ToolContractDigest: "sha256:" + strings.Repeat("3", 64), Principal: "operator",
		CredentialLabels: []string{"database-writer"}, RunIdentity: f.identity("run"),
		SessionIdentity: f.identity("session"), ContextIdentity: f.identity("context"),
		ContextProvenance: action.ProvenanceOperatorBound,
	}
	fields := []actionledger.SelectedFieldEvidence{}
	if selected {
		fields = append(fields, actionledger.SelectedFieldEvidence{
			Source: action.SourceArguments, PointerIdentity: f.identity("pointer"),
			State: action.PointerPresent, Kind: action.ValueString,
			ValueIdentity: f.identity("value"), ByteLength: 3, ItemCount: 0, Complete: true,
		})
	}
	request := actionledger.Record{
		Timestamp: requestTime.Format(time.RFC3339Nano), Event: actionledger.EventRequestAccepted,
		Call: binding,
		Decision: actionledger.DecisionBinding{
			Phase: action.PhasePreCall, RuleIDs: []string{}, Completeness: action.CompleteEvidence(),
		},
		SelectedFields:  fields,
		RequestAccepted: &actionledger.RequestAccepted{ArgumentBytes: 2, ArgumentItems: 0},
	}
	if _, err := f.store.Append(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	decision := actionledger.Record{
		Timestamp: decisionTime.Format(time.RFC3339Nano), Event: actionledger.EventPreDecision,
		Call: binding,
		Decision: actionledger.DecisionBinding{
			Phase: action.PhasePreCall, Decision: action.DecisionBlock,
			Reason: action.ReasonRuleMatched, RuleIDs: []string{"block-production"},
			Completeness: action.CompleteEvidence(),
		},
		SelectedFields: fields,
		PreDecision:    &actionledger.PreDecision{Outcome: action.OutcomeDispatchBlocked},
	}
	if _, err := f.store.Append(context.Background(), decision); err != nil {
		t.Fatal(err)
	}
}

func TestBuildExportsOnlyVerifiedMinimizedReproductions(t *testing.T) {
	fixture := newExportFixture(t)
	fixture.appendBlockedCall(t, "act_aaaaaaaaaaaaaaaaaaaaaaaaaa", false, action.LedgerExactName)
	fixture.appendBlockedCall(t, "act_bbbbbbbbbbbbbbbbbbbbbbbbbb", true, action.LedgerExactName)
	report, err := Build(
		context.Background(), fixture.store, fixture.repo, actionledger.Filter{}, fixture.compiled,
	)
	if err != nil {
		t.Fatal(err)
	}
	if report.SelectedCalls != 2 || report.ExportedCases != 1 || len(report.OmittedCalls) != 1 ||
		report.OmittedCalls[0].Reason != OmitSelectedFields || report.Corpus == nil ||
		len(report.Corpus.Cases) != 1 || report.ReplayComplete || report.LifecycleCoverageComplete {
		t.Fatalf("Build() = %#v", report)
	}
	if report.Corpus.Completeness.CompleteReplay {
		t.Fatal("minimized ledger export claimed complete replay")
	}
	ledger := report.Corpus.Cases[0].Action.Expected.Ledger
	if ledger == nil || ledger.Mode != action.LedgerRequired || !ledger.Required ||
		ledger.Event != actionledger.EventPreDecision {
		t.Fatalf("exported ledger assertion = %#v", ledger)
	}
	body, err := Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	second, err := Marshal(report)
	if err != nil || !bytes.Equal(body, second) || bytes.Contains(body, []byte("database-password")) ||
		!bytes.Contains(body, []byte(`"derivation": "synthetic_minimized_verified"`)) {
		t.Fatalf("export encoding is invalid: %v\n%s", err, body)
	}
}

func TestBuildFilterAndPolicyDriftProduceExplicitOmissions(t *testing.T) {
	fixture := newExportFixture(t)
	callID := "act_cccccccccccccccccccccccccc"
	fixture.appendBlockedCall(t, callID, false, action.LedgerExactName)
	drifted := fixture.compiled
	drifted.LockDigest = strings.Repeat("9", 64)
	report, err := Build(
		context.Background(), fixture.store, fixture.repo,
		actionledger.Filter{CallID: callID}, drifted,
	)
	if err != nil {
		t.Fatal(err)
	}
	if report.SelectedCalls != 1 || report.ExportedCases != 0 || report.Corpus != nil ||
		len(report.OmittedCalls) != 1 || report.OmittedCalls[0].Reason != OmitPolicyChanged {
		t.Fatalf("drifted Build() = %#v", report)
	}
	report.ReplayComplete = true
	if _, err := Marshal(report); err == nil {
		t.Fatal("Marshal() accepted a replay-complete claim")
	}
}

func TestBuildDoesNotExpandRedactedToolIdentity(t *testing.T) {
	fixture := newExportFixture(t)
	callID := "act_dddddddddddddddddddddddddd"
	fixture.appendBlockedCall(t, callID, false, action.LedgerDeclarationID)
	report, err := Build(
		context.Background(), fixture.store, fixture.repo,
		actionledger.Filter{CallID: callID}, fixture.compiled,
	)
	if err != nil {
		t.Fatal(err)
	}
	if report.ExportedCases != 0 || report.Corpus != nil || len(report.OmittedCalls) != 1 ||
		report.OmittedCalls[0].Reason != OmitToolIdentityUnavailable {
		t.Fatalf("redacted tool identity export = %#v", report)
	}
}

func TestMarshalRejectsEveryContradictoryExportClaim(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Report)
	}{
		{name: "format", mutate: func(report *Report) { report.FormatVersion = "other" }},
		{name: "derivation", mutate: func(report *Report) { report.Derivation = "captured_raw" }},
		{name: "missing dimensions", mutate: func(report *Report) {
			report.MissingDimensions = []string{"original_arguments"}
		}},
		{name: "replay complete", mutate: func(report *Report) { report.ReplayComplete = true }},
		{name: "coverage", mutate: func(report *Report) { report.LifecycleCoverageComplete = true }},
		{name: "count", mutate: func(report *Report) { report.SelectedCalls = 1 }},
		{name: "verification", mutate: func(report *Report) { report.Verification.FormatVersion = "other" }},
		{name: "false verified ledger", mutate: func(report *Report) {
			report.Verification.Integrity = actionledger.StatusVerified
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			report := EmptyReport()
			test.mutate(&report)
			if _, err := Marshal(report); err == nil {
				t.Fatal("contradictory export report was accepted")
			}
		})
	}
	report := EmptyReport()
	report.SelectedCalls = 1
	report.OmittedCalls = []Omission{{
		CallID: "act_aaaaaaaaaaaaaaaaaaaaaaaaaa", Reason: "invented",
	}}
	if _, err := Marshal(report); err == nil {
		t.Fatal("unknown omission reason was accepted")
	}

	fixture := newExportFixture(t)
	fixture.appendBlockedCall(t, "act_eeeeeeeeeeeeeeeeeeeeeeeeee", false, action.LedgerExactName)
	built, err := Build(context.Background(), fixture.store, fixture.repo, actionledger.Filter{}, fixture.compiled)
	if err != nil {
		t.Fatal(err)
	}
	built.Corpus.Cases[0].Action.Request.Arguments = impactlab.ActionPayload(`{"ordinary":"raw"}`)
	if _, err := Marshal(built); err == nil {
		t.Fatal("Marshal() accepted injected raw arguments in a minimized export")
	}
}
