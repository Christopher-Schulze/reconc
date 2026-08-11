package actionstate

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"reconc.dev/reconc/internal/action"
	"reconc.dev/reconc/internal/actionapproval"
)

type issuedApprovalFixture struct {
	binding    ApprovalBinding
	issue      ApprovalIssueResult
	registry   LoadedApprovalRegistry
	privateKey ed25519.PrivateKey
}

func issueFixtureApproval(
	t testing.TB,
	fixture *storeFixture,
	input ReserveRequest,
	reserved ReserveResult,
) issuedApprovalFixture {
	t.Helper()
	request := input.Request
	request.StateVersion = reserved.Snapshot.StateVersion
	binding := ApprovalBinding{
		Plan: input.Plan, Context: input.Context, Authority: input.Authority, Server: input.Server,
		Evaluation: action.EvaluationInput{
			Request: request, SourceIdentity: strings.Repeat("8", 64),
			ContextIdentity: input.Context.ContextIdentity, ExecutableDigest: input.Server.ExecutableDigest,
			Principal: input.Context.Principal, CredentialLabels: credentialLabels(input.Context.Credentials),
			Budget:    reserved.Snapshot,
			Approval:  action.ApprovalSnapshot{Status: action.ApprovalNone, Identity: "approval-none"},
			Taint:     action.TaintSnapshot{Status: action.TaintClean, Identity: "taint-clean"},
			Lifecycle: action.LifecycleActive, CachePolicyVersion: action.CacheIdentityVersion,
		},
	}
	issue, err := fixture.store.IssueApproval(context.Background(), ApprovalIssueRequest{
		Binding: binding, AuthorityPolicyID: "production-writes", TTL: 30 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	seed := bytes.Repeat([]byte{0x19}, ed25519.SeedSize)
	privateKey := ed25519.NewKeyFromSeed(seed)
	now, err := fixture.clock.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	compiledRegistry, err := actionapproval.CompileRegistry(actionapproval.Registry{
		Schema: actionapproval.RegistrySchema, FormatVersion: actionapproval.FormatVersion,
		Authorities: []actionapproval.Authority{{
			ID:         "security-primary",
			PublicKey:  base64.RawURLEncoding.EncodeToString(privateKey.Public().(ed25519.PublicKey)),
			ActiveFrom: now.Time.Add(-time.Hour).UTC().Format(time.RFC3339Nano),
		}},
		AuthorityPolicies: []actionapproval.AuthorityPolicy{{
			ID: "production-writes", AuthorityKeyIDs: []string{"security-primary"},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	registry := LoadedApprovalRegistry{
		path: "test-operator-registry", identity: compiledRegistry.Identity(), registry: compiledRegistry,
	}
	return issuedApprovalFixture{binding: binding, issue: issue, registry: registry, privateKey: privateKey}
}

func consumeFixtureApproval(
	t testing.TB,
	fixture *storeFixture,
	issued issuedApprovalFixture,
	decision actionapproval.Decision,
) (ApprovalConsumeResult, error) {
	t.Helper()
	receiptEntropy := issued.issue.Request.CallID[len(issued.issue.Request.CallID)-1]
	signedAt, err := time.Parse(time.RFC3339Nano, issued.issue.Request.IssuedAt)
	if err != nil {
		t.Fatal(err)
	}
	_, receipt, err := actionapproval.SignReceipt(
		issued.issue.Request, "security-primary", issued.privateKey, decision,
		signedAt,
		bytes.NewReader(bytes.Repeat([]byte{receiptEntropy}, 16)),
	)
	if err != nil {
		t.Fatal(err)
	}
	binding := issued.binding
	binding.Evaluation.Request.StateVersion = issued.issue.StateVersion
	return fixture.store.ConsumeApproval(context.Background(), ApprovalConsumeRequest{
		Binding: binding, Registry: issued.registry, Receipt: receipt,
		RequestState: issued.issue.RequestState, ExpectedStateVersion: issued.issue.StateVersion,
	})
}

func TestApprovalStoreIssuesRedactedRequestAndConsumesReceiptOnce(t *testing.T) {
	fixture := newStoreFixture(t, []action.Budget{storeBudget(
		"approval", action.BudgetLimits{CallCount: 2, ApprovalCount: 2}, action.BudgetResetNever,
	)})
	input, reserved := fixture.reserve(t, callID("a"))
	issued := issueFixtureApproval(t, fixture, input, reserved)
	body, err := actionapproval.EncodeRequest(issued.issue.Request)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(body, []byte("staging")) || len(issued.issue.Request.SelectedArguments) != 1 ||
		issued.issue.Request.SelectedArguments[0].Pointer != "/target" ||
		!action.ValidKeyedIdentity(issued.issue.Request.SelectedArguments[0].Identity) {
		t.Fatalf("approval request leaked or omitted selected evidence: %s", body)
	}
	evidenceBody, err := json.Marshal(issued.issue.Evidence)
	if err != nil {
		t.Fatal(err)
	}
	if issued.issue.Evidence.Schema != ApprovalEvidenceSchema ||
		issued.issue.Evidence.Status != actionapproval.StatusPending ||
		issued.issue.Evidence.RequestID != issued.issue.Request.RequestID ||
		bytes.Contains(evidenceBody, []byte("staging")) {
		t.Fatalf("pending approval evidence = %s", evidenceBody)
	}
	consumed, err := consumeFixtureApproval(t, fixture, issued, actionapproval.DecisionApprove)
	if err != nil {
		t.Fatal(err)
	}
	if consumed.Status != actionapproval.StatusApproved ||
		consumed.Approval.Status != action.ApprovalConsumed ||
		!action.ValidSHA256Identity(consumed.ReceiptIdentity) || consumed.ReceiptSignedAt == "" ||
		consumed.AuthorityKeyID != "security-primary" {
		t.Fatalf("consumed approval = %#v", consumed)
	}
	if consumed.Evidence.Decision != actionapproval.DecisionApprove ||
		consumed.Evidence.AuthorityKeyID != consumed.AuthorityKeyID ||
		consumed.Evidence.ReceiptIdentity != consumed.ReceiptIdentity ||
		consumed.Evidence.ReceiptSignedAt != consumed.ReceiptSignedAt {
		t.Fatalf("consumed approval evidence = %#v", consumed.Evidence)
	}
	binding := issued.binding
	binding.Evaluation.Request.StateVersion = consumed.StateVersion
	_, err = fixture.store.ConsumeApproval(context.Background(), ApprovalConsumeRequest{
		Binding: binding, Registry: issued.registry, Receipt: []byte(`{}`),
		RequestState: issued.issue.RequestState, ExpectedStateVersion: consumed.StateVersion,
	})
	requireStateCode(t, err, action.ReasonApprovalReplayed)
}

func TestApprovalRequestStateRejectsEquivalentNonCanonicalJSON(t *testing.T) {
	fixture := newStoreFixture(t, []action.Budget{storeBudget(
		"approval-state-canonical", action.BudgetLimits{CallCount: 2, ApprovalCount: 2}, action.BudgetResetNever,
	)})
	input, reserved := fixture.reserve(t, callID("f"))
	issued := issueFixtureApproval(t, fixture, input, reserved)
	body, err := base64.RawURLEncoding.Strict().DecodeString(issued.issue.RequestState)
	if err != nil {
		t.Fatal(err)
	}
	var indented bytes.Buffer
	if err := json.Indent(&indented, body, "", "  "); err != nil {
		t.Fatal(err)
	}
	nonCanonical := base64.RawURLEncoding.EncodeToString(indented.Bytes())
	if nonCanonical == issued.issue.RequestState {
		t.Fatal("non-canonical request state fixture did not change")
	}
	if _, err := fixture.store.openApprovalRequestState(nonCanonical); err == nil {
		t.Fatal("equivalent non-canonical request state was accepted")
	}
}

func TestApprovalStoreRecordsSignedRejectionAndReleasesReservation(t *testing.T) {
	fixture := newStoreFixture(t, []action.Budget{storeBudget(
		"rejection", action.BudgetLimits{CallCount: 2, ApprovalCount: 2}, action.BudgetResetNever,
	)})
	input, reserved := fixture.reserve(t, callID("b"))
	issued := issueFixtureApproval(t, fixture, input, reserved)
	result, err := consumeFixtureApproval(t, fixture, issued, actionapproval.DecisionReject)
	requireStateCode(t, err, action.ReasonApprovalRejected)
	if result.Status != actionapproval.StatusRejected || result.ReceiptID == "" ||
		result.Evidence.Decision != actionapproval.DecisionReject {
		t.Fatalf("signed rejection = %#v", result)
	}
	status, err := fixture.store.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if status.LiveReservations != 0 || status.TerminalCallCount != 1 ||
		len(status.ApprovalRecords) != 1 || status.ApprovalRecords[0].Status != actionapproval.StatusRejected {
		t.Fatalf("rejected approval state = %#v", status)
	}
}

func TestApprovalStoreExpiresAndTerminalizesPendingRequest(t *testing.T) {
	fixture := newStoreFixture(t, []action.Budget{storeBudget(
		"expiry", action.BudgetLimits{CallCount: 2, ApprovalCount: 2}, action.BudgetResetNever,
	)})
	input, reserved := fixture.reserve(t, callID("c"))
	issued := issueFixtureApproval(t, fixture, input, reserved)
	fixture.clock.set(time.Date(2026, 8, 11, 12, 1, 0, 0, time.UTC), "test-clock")
	result, err := consumeFixtureApproval(t, fixture, issued, actionapproval.DecisionApprove)
	requireStateCode(t, err, action.ReasonApprovalExpired)
	if result.Status != actionapproval.StatusExpired || result.StateVersion == "" {
		t.Fatalf("expired approval = %#v", result)
	}
	status, statusErr := fixture.store.Status(context.Background())
	if statusErr != nil {
		t.Fatal(statusErr)
	}
	if status.PendingApprovals != 0 || status.LiveReservations != 0 || status.TerminalCallCount != 1 {
		t.Fatalf("expired approval state = %#v", status)
	}
}

func TestApprovalStoreReconcilesEveryExpiredCrashOrphanAtomically(t *testing.T) {
	fixture := newStoreFixture(t, []action.Budget{storeBudget(
		"crash-expiry", action.BudgetLimits{CallCount: 4, ApprovalCount: 4}, action.BudgetResetNever,
	)})
	for _, suffix := range []string{"d", "e"} {
		input, reserved := fixture.reserve(t, callID(suffix))
		issueFixtureApproval(t, fixture, input, reserved)
	}
	fixture.clock.set(time.Date(2026, 8, 11, 12, 1, 0, 0, time.UTC), "test-clock")
	reconciled, err := fixture.store.ReconcileExpiredApprovals(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(reconciled.Expired) != 2 || reconciled.Expired[0].Status != actionapproval.StatusExpired ||
		reconciled.Expired[1].Status != actionapproval.StatusExpired ||
		reconciled.Expired[0].Evidence.Status != actionapproval.StatusExpired ||
		reconciled.Expired[1].Evidence.Status != actionapproval.StatusExpired {
		t.Fatalf("reconciled approvals = %#v", reconciled)
	}
	status, err := fixture.store.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if status.PendingApprovals != 0 || status.LiveReservations != 0 || status.TerminalCallCount != 2 {
		t.Fatalf("reconciled approval state = %#v", status)
	}
	again, err := fixture.store.ReconcileExpiredApprovals(context.Background())
	if err != nil || len(again.Expired) != 0 || again.StateVersion != reconciled.StateVersion {
		t.Fatalf("idempotent reconciliation = %#v, %v", again, err)
	}
}
