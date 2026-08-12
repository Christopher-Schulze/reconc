package actionstate

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"reconc.dev/reconc/internal/action"
	"reconc.dev/reconc/internal/actionapproval"
)

func completePersistedState(t testing.TB) (*storeFixture, State) {
	t.Helper()
	limits := action.BudgetLimits{
		CallCount: 10, DeniedCount: 10, ApprovalCount: 10,
		ArgumentBytes: 10_000, ResultBytes: 10_000, CostUnits: 100,
		Concurrent: 4,
	}
	budget := storeBudget("complete-state", limits, action.BudgetResetOperatorRun)
	fixture := newStoreFixture(t, []action.Budget{budget})
	firstInput, first := fixture.reserve(t, callID("c"))
	firstIssued := issueFixtureApproval(t, fixture, firstInput, first)
	firstApproval, err := consumeFixtureApproval(t, fixture, firstIssued, actionapproval.DecisionApprove)
	if err != nil {
		t.Fatal(err)
	}
	version := firstApproval.StateVersion
	version, err = fixture.store.MarkDispatched(context.Background(), first.Reservation.Identity, version)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.store.Settle(
		context.Background(), first.Reservation.Identity, version, OutcomeSucceeded, 37,
	); err != nil {
		t.Fatal(err)
	}
	secondInput, second := fixture.reserve(t, callID("d"))
	secondIssued := issueFixtureApproval(t, fixture, secondInput, second)
	if _, err := consumeFixtureApproval(t, fixture, secondIssued, actionapproval.DecisionApprove); err != nil {
		t.Fatal(err)
	}

	var state State
	if err := fixture.store.withLock(context.Background(), func() error {
		var loadErr error
		state, _, loadErr = fixture.store.loadState()
		return loadErr
	}); err != nil {
		t.Fatal(err)
	}
	if len(state.Budgets) != 1 || len(state.Reservations) != 1 || len(state.TerminalCalls) != 1 ||
		len(state.Budgets[0].GenerationHistory) != 1 || len(state.Reservations[0].Charges) != 1 {
		t.Fatalf("complete state fixture is incomplete: %#v", state)
	}
	return fixture, state
}

func TestStateDigestBindsEveryPersistedComponent(t *testing.T) {
	fixture, baseline := completePersistedState(t)
	keyed := func(name string) string { return fixture.key.Identity(DomainBudget, []byte(name)) }
	sha := func(character string) string { return "sha256:" + strings.Repeat(character, 64) }
	policy := strings.Repeat("9", 64)
	tests := []struct {
		name   string
		mutate func(*State)
	}{
		{name: "schema", mutate: func(state *State) { state.Schema = "other" }},
		{name: "format version", mutate: func(state *State) { state.FormatVersion = "2" }},
		{name: "key ID", mutate: func(state *State) { state.KeyID = strings.Repeat("9", 32) }},
		{name: "repository", mutate: func(state *State) { state.RepositoryIdentity = keyed("repository") }},
		{name: "revision", mutate: func(state *State) { state.Revision++ }},
		{name: "clock source", mutate: func(state *State) { state.ClockSource = "other-clock" }},
		{name: "clock value", mutate: func(state *State) { state.LastObservedUnixNano++ }},
		{name: "budget ID", mutate: func(state *State) { state.Budgets[0].BudgetID = "other-budget" }},
		{name: "scope identity", mutate: func(state *State) { state.Budgets[0].ScopeIdentity = keyed("scope") }},
		{name: "lineage identity", mutate: func(state *State) { state.Budgets[0].LineageIdentity = keyed("lineage") }},
		{name: "scope repository", mutate: func(state *State) { state.Budgets[0].Scope.RepositoryIdentity = keyed("scope-repository") }},
		{name: "scope principal", mutate: func(state *State) { state.Budgets[0].Scope.Principal = "other-principal" }},
		{name: "scope credential", mutate: func(state *State) { state.Budgets[0].Scope.CredentialLabels[0] = "other-credential" }},
		{name: "scope server label", mutate: func(state *State) { state.Budgets[0].Scope.ServerLabel = "other-server" }},
		{name: "scope server identity", mutate: func(state *State) { state.Budgets[0].Scope.ServerIdentity = keyed("server") }},
		{name: "scope tool", mutate: func(state *State) { state.Budgets[0].Scope.ToolID = "other-tool" }},
		{name: "scope run", mutate: func(state *State) { state.Budgets[0].Scope.RunIdentity = keyed("run") }},
		{name: "scope session", mutate: func(state *State) { state.Budgets[0].Scope.SessionIdentity = keyed("session") }},
		{name: "scope window", mutate: func(state *State) { state.Budgets[0].Scope.WindowIdentity = keyed("window") }},
		{name: "scope window start", mutate: func(state *State) { state.Budgets[0].Scope.WindowStartUnix++ }},
		{name: "reset", mutate: func(state *State) { state.Budgets[0].Reset = action.BudgetResetNever }},
		{name: "window seconds", mutate: func(state *State) { state.Budgets[0].WindowSeconds++ }},
		{name: "limit call", mutate: func(state *State) { state.Budgets[0].Limits.CallCount++ }},
		{name: "limit denied", mutate: func(state *State) { state.Budgets[0].Limits.DeniedCount++ }},
		{name: "limit approval", mutate: func(state *State) { state.Budgets[0].Limits.ApprovalCount++ }},
		{name: "limit argument", mutate: func(state *State) { state.Budgets[0].Limits.ArgumentBytes++ }},
		{name: "limit result", mutate: func(state *State) { state.Budgets[0].Limits.ResultBytes++ }},
		{name: "limit cost", mutate: func(state *State) { state.Budgets[0].Limits.CostUnits++ }},
		{name: "limit concurrent", mutate: func(state *State) { state.Budgets[0].Limits.Concurrent-- }},
		{name: "limit rate", mutate: func(state *State) { state.Budgets[0].Limits.RateWindow++ }},
		{name: "consumed call", mutate: func(state *State) { state.Budgets[0].Consumed.CallCount++ }},
		{name: "consumed denied", mutate: func(state *State) { state.Budgets[0].Consumed.DeniedCount++ }},
		{name: "consumed approval", mutate: func(state *State) { state.Budgets[0].Consumed.ApprovalCount++ }},
		{name: "consumed argument", mutate: func(state *State) { state.Budgets[0].Consumed.ArgumentBytes++ }},
		{name: "consumed result", mutate: func(state *State) { state.Budgets[0].Consumed.ResultBytes++ }},
		{name: "consumed cost", mutate: func(state *State) { state.Budgets[0].Consumed.CostUnits++ }},
		{name: "consumed concurrent", mutate: func(state *State) { state.Budgets[0].Consumed.Concurrent++ }},
		{name: "consumed rate", mutate: func(state *State) { state.Budgets[0].Consumed.RateWindow++ }},
		{name: "generation policy", mutate: func(state *State) { state.Budgets[0].Generation.PolicyDigest = policy }},
		{name: "generation executable", mutate: func(state *State) { state.Budgets[0].Generation.ExecutableDigest = sha("8") }},
		{name: "generation tool", mutate: func(state *State) { state.Budgets[0].Generation.ToolContractDigest = sha("7") }},
		{name: "generation key", mutate: func(state *State) { state.Budgets[0].Generation.KeyID = strings.Repeat("8", 32) }},
		{name: "generation history", mutate: func(state *State) { state.Budgets[0].GenerationHistory[0].PolicyDigest = policy }},
		{name: "reservation identity", mutate: func(state *State) { state.Reservations[0].Identity = keyed("reservation") }},
		{name: "reservation call", mutate: func(state *State) { state.Reservations[0].CallID = callID("e") }},
		{name: "reservation owner", mutate: func(state *State) { state.Reservations[0].OwnerID = "other-owner" }},
		{name: "reservation request", mutate: func(state *State) { state.Reservations[0].RequestIdentity = keyed("request") }},
		{name: "reservation context", mutate: func(state *State) { state.Reservations[0].ContextIdentity = keyed("context") }},
		{name: "reservation executable", mutate: func(state *State) { state.Reservations[0].ExecutableDigest = sha("5") }},
		{name: "reservation status", mutate: func(state *State) { state.Reservations[0].Status = ReservationIndeterminate }},
		{name: "reservation created", mutate: func(state *State) { state.Reservations[0].CreatedAtUnix-- }},
		{name: "reservation updated", mutate: func(state *State) { state.Reservations[0].UpdatedAtUnix++ }},
		{name: "charge budget", mutate: func(state *State) { state.Reservations[0].Charges[0].BudgetID = "other-budget" }},
		{name: "charge scope", mutate: func(state *State) { state.Reservations[0].Charges[0].ScopeIdentity = keyed("charge-scope") }},
		{name: "charge lineage", mutate: func(state *State) { state.Reservations[0].Charges[0].LineageIdentity = keyed("charge-lineage") }},
		{name: "charge generation", mutate: func(state *State) { state.Reservations[0].Charges[0].Generation.PolicyDigest = policy }},
		{name: "charge reserved", mutate: func(state *State) { state.Reservations[0].Charges[0].Reserved.CallCount++ }},
		{name: "charge dispatch", mutate: func(state *State) { state.Reservations[0].Charges[0].DispatchCommitted = true }},
		{name: "charge approval", mutate: func(state *State) { state.Reservations[0].Charges[0].CommittedApprovals-- }},
		{name: "terminal call", mutate: func(state *State) { state.TerminalCalls[0].CallID = callID("f") }},
		{name: "terminal reservation", mutate: func(state *State) { state.TerminalCalls[0].ReservationIdentity = keyed("terminal") }},
		{name: "terminal outcome", mutate: func(state *State) { state.TerminalCalls[0].Outcome = OutcomeFailed }},
		{name: "terminal time", mutate: func(state *State) { state.TerminalCalls[0].CompletedAtUnix++ }},
		{name: "approval request schema", mutate: func(state *State) { state.Approvals[0].Request.Schema = "other" }},
		{name: "approval request format", mutate: func(state *State) { state.Approvals[0].Request.FormatVersion = "2" }},
		{name: "approval request id", mutate: func(state *State) { state.Approvals[0].Request.RequestID = "apr_" + strings.Repeat("z", 26) }},
		{name: "approval call id", mutate: func(state *State) { state.Approvals[0].Request.CallID = callID("g") }},
		{name: "approval request identity", mutate: func(state *State) { state.Approvals[0].Request.RequestIdentity = keyed("approval-request") }},
		{name: "approval requirement", mutate: func(state *State) { state.Approvals[0].Request.RequiredApprovalIdentity = sha("4") }},
		{name: "approval plan", mutate: func(state *State) { state.Approvals[0].Request.PlanIdentity = sha("3") }},
		{name: "approval source", mutate: func(state *State) { state.Approvals[0].Request.SourceIdentity = policy }},
		{name: "approval repository", mutate: func(state *State) { state.Approvals[0].Request.RepositoryIdentity = keyed("approval-repository") }},
		{name: "approval state", mutate: func(state *State) { state.Approvals[0].Request.StateVersion = keyed("approval-state") }},
		{name: "approval policy", mutate: func(state *State) { state.Approvals[0].Request.PolicyDigest = policy }},
		{name: "approval lock", mutate: func(state *State) { state.Approvals[0].Request.LockDigest = policy }},
		{name: "approval executable", mutate: func(state *State) { state.Approvals[0].Request.ExecutableDigest = sha("2") }},
		{name: "approval server label", mutate: func(state *State) { state.Approvals[0].Request.ServerLabel = "other-server" }},
		{name: "approval server fingerprint", mutate: func(state *State) { state.Approvals[0].Request.ServerFingerprint = keyed("approval-server") }},
		{name: "approval tool id", mutate: func(state *State) { state.Approvals[0].Request.ToolID = "other-tool" }},
		{name: "approval tool", mutate: func(state *State) { state.Approvals[0].Request.Tool = "other.execute" }},
		{name: "approval tool contract", mutate: func(state *State) { state.Approvals[0].Request.ToolContractDigest = sha("1") }},
		{name: "approval phase", mutate: func(state *State) { state.Approvals[0].Request.Phase = action.PhasePostResult }},
		{name: "approval principal", mutate: func(state *State) { state.Approvals[0].Request.Principal = "other-principal" }},
		{name: "approval context", mutate: func(state *State) { state.Approvals[0].Request.ContextIdentity = keyed("approval-context") }},
		{name: "approval credential", mutate: func(state *State) { state.Approvals[0].Request.CredentialLabels[0] = "other-credential" }},
		{name: "approval taint", mutate: func(state *State) { state.Approvals[0].Request.TaintIdentity = "taint-other" }},
		{name: "approval repository effect", mutate: func(state *State) { state.Approvals[0].Request.RepositoryEffectIdentity = "effect-other" }},
		{name: "approval selected pointer", mutate: func(state *State) { state.Approvals[0].Request.SelectedArguments[0].Pointer = "/other" }},
		{name: "approval selected state", mutate: func(state *State) { state.Approvals[0].Request.SelectedArguments[0].State = action.PointerNull }},
		{name: "approval selected kind", mutate: func(state *State) { state.Approvals[0].Request.SelectedArguments[0].Kind = action.ValueObject }},
		{name: "approval selected bytes", mutate: func(state *State) { state.Approvals[0].Request.SelectedArguments[0].ByteLength++ }},
		{name: "approval selected identity", mutate: func(state *State) {
			state.Approvals[0].Request.SelectedArguments[0].Identity = keyed("approval-argument")
		}},
		{name: "approval budget reservation", mutate: func(state *State) { state.Approvals[0].Request.BudgetReservationID = keyed("approval-budget") }},
		{name: "approval reason", mutate: func(state *State) { state.Approvals[0].Request.ReasonCode = action.ReasonConditionIndeterminate }},
		{name: "approval rule", mutate: func(state *State) { state.Approvals[0].Request.RuleIDs[0] = "other-rule" }},
		{name: "approval authority policy", mutate: func(state *State) { state.Approvals[0].Request.AuthorityPolicyID = "other-policy" }},
		{name: "approval issued time", mutate: func(state *State) { state.Approvals[0].Request.IssuedAt = state.Approvals[0].Request.ExpiresAt }},
		{name: "approval expiry", mutate: func(state *State) { state.Approvals[0].Request.ExpiresAt = state.Approvals[0].Request.IssuedAt }},
		{name: "approval nonce", mutate: func(state *State) { state.Approvals[0].Request.Nonce = state.Approvals[1].Request.Nonce }},
		{name: "approval status", mutate: func(state *State) { state.Approvals[0].Status = actionapproval.StatusRejected }},
		{name: "approval reservation", mutate: func(state *State) { state.Approvals[0].ReservationIdentity = keyed("approval-record-reservation") }},
		{name: "approval nonce identity", mutate: func(state *State) { state.Approvals[0].NonceIdentity = keyed("approval-nonce") }},
		{name: "approval registry", mutate: func(state *State) { state.Approvals[0].RegistryIdentity = sha("0") }},
		{name: "approval authority key", mutate: func(state *State) { state.Approvals[0].AuthorityKeyID = "other-authority" }},
		{name: "approval receipt id", mutate: func(state *State) { state.Approvals[0].ReceiptID = "arc_" + strings.Repeat("z", 26) }},
		{name: "approval receipt identity", mutate: func(state *State) { state.Approvals[0].ReceiptIdentity = sha("f") }},
		{name: "approval receipt signed time", mutate: func(state *State) { state.Approvals[0].ReceiptSignedAt = state.Approvals[0].Request.ExpiresAt }},
		{name: "approval receipt decision", mutate: func(state *State) { state.Approvals[0].ReceiptDecision = actionapproval.DecisionReject }},
		{name: "approval receipt signature", mutate: func(state *State) {
			prefix := "A"
			if state.Approvals[0].ReceiptSignature[0] == 'A' {
				prefix = "B"
			}
			state.Approvals[0].ReceiptSignature = prefix + state.Approvals[0].ReceiptSignature[1:]
		}},
		{name: "approval updated time", mutate: func(state *State) { state.Approvals[0].UpdatedAtUnix++ }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			mutated := cloneState(baseline)
			test.mutate(&mutated)
			digest, err := fixture.store.stateDigest(mutated)
			if err != nil {
				t.Fatal(err)
			}
			if digest == baseline.Digest {
				t.Fatal("persisted component is absent from the complete state fingerprint")
			}
		})
	}
}

func TestStateSemanticValidationRejectsMalformedStateWithFreshDigest(t *testing.T) {
	fixture, baseline := completePersistedState(t)
	keyed := func(name string) string { return fixture.key.Identity(DomainApproval, []byte(name)) }
	sha := func(character string) string { return "sha256:" + strings.Repeat(character, 64) }
	approvalForReservation := func(state *State, identity string) *ApprovalRecord {
		for index := range state.Approvals {
			if state.Approvals[index].ReservationIdentity == identity {
				return &state.Approvals[index]
			}
		}
		return nil
	}
	clearReceipt := func(record *ApprovalRecord) {
		record.RegistryIdentity = ""
		record.AuthorityKeyID = ""
		record.ReceiptID = ""
		record.ReceiptIdentity = ""
		record.ReceiptSignedAt = ""
		record.ReceiptDecision = ""
		record.ReceiptSignature = ""
	}
	tests := []struct {
		name   string
		mutate func(*State)
	}{
		{name: "zero revision", mutate: func(state *State) { state.Revision = 0 }},
		{name: "missing clock", mutate: func(state *State) { state.ClockSource = "" }},
		{name: "nil budgets", mutate: func(state *State) { state.Budgets = nil }},
		{name: "nil reservations", mutate: func(state *State) { state.Reservations = nil }},
		{name: "nil terminal calls", mutate: func(state *State) { state.TerminalCalls = nil }},
		{name: "nil approvals", mutate: func(state *State) { state.Approvals = nil }},
		{name: "invalid budget ID", mutate: func(state *State) { state.Budgets[0].BudgetID = "Invalid" }},
		{name: "same scope and lineage", mutate: func(state *State) { state.Budgets[0].ScopeIdentity = state.Budgets[0].LineageIdentity }},
		{name: "nil credential labels", mutate: func(state *State) { state.Budgets[0].Scope.CredentialLabels = nil }},
		{name: "missing run identity", mutate: func(state *State) { state.Budgets[0].Scope.RunIdentity = "absent" }},
		{name: "empty generation history", mutate: func(state *State) { state.Budgets[0].GenerationHistory = []action.BudgetGeneration{} }},
		{name: "consumed concurrency", mutate: func(state *State) { state.Budgets[0].Consumed.Concurrent = 1 }},
		{name: "invalid reservation call", mutate: func(state *State) { state.Reservations[0].CallID = "call" }},
		{name: "empty reservation charges", mutate: func(state *State) { state.Reservations[0].Charges = []ReservationCharge{} }},
		{name: "reservation time inversion", mutate: func(state *State) { state.Reservations[0].UpdatedAtUnix = state.Reservations[0].CreatedAtUnix - 1 }},
		{name: "invalid charge budget", mutate: func(state *State) { state.Reservations[0].Charges[0].BudgetID = "other" }},
		{name: "invalid pre-dispatch commit", mutate: func(state *State) { state.Reservations[0].Charges[0].DispatchCommitted = true }},
		{name: "active terminal call", mutate: func(state *State) { state.TerminalCalls[0].CallID = state.Reservations[0].CallID }},
		{name: "invalid terminal outcome", mutate: func(state *State) { state.TerminalCalls[0].Outcome = "unknown" }},
		{name: "invalid approval request", mutate: func(state *State) { state.Approvals[0].Request.Schema = "other" }},
		{name: "approval repository mismatch", mutate: func(state *State) { state.Approvals[0].Request.RepositoryIdentity = keyed("other-repository") }},
		{name: "duplicate approval call", mutate: func(state *State) { state.Approvals[1].Request.CallID = state.Approvals[0].Request.CallID }},
		{name: "duplicate approval nonce", mutate: func(state *State) { state.Approvals[1].NonceIdentity = state.Approvals[0].NonceIdentity }},
		{name: "invalid selected approval key", mutate: func(state *State) { state.Approvals[0].Request.SelectedArguments[0].Identity = sha("a") }},
		{name: "missing verified metadata", mutate: func(state *State) { state.Approvals[0].ReceiptIdentity = "" }},
		{name: "invalid verified receipt time", mutate: func(state *State) { state.Approvals[0].ReceiptSignedAt = state.Approvals[0].Request.ExpiresAt }},
		{name: "metadata on unverified status", mutate: func(state *State) { state.Approvals[0].Status = actionapproval.StatusMalformed }},
		{name: "pending approval has committed charge", mutate: func(state *State) {
			record := approvalForReservation(state, state.Reservations[0].Identity)
			record.Status = actionapproval.StatusPending
			clearReceipt(record)
		}},
		{name: "approved approval has uncommitted charge", mutate: func(state *State) {
			state.Reservations[0].Charges[0].CommittedApprovals = 0
			state.Reservations[0].Charges[0].Reserved.ApprovalCount = 1
		}},
		{name: "approved approval loses reservation linkage", mutate: func(state *State) {
			record := approvalForReservation(state, state.Reservations[0].Identity)
			record.ReservationIdentity = keyed("missing-approval-reservation")
			record.Request.BudgetReservationID = record.ReservationIdentity
		}},
		{name: "failed approval lacks terminal call", mutate: func(state *State) {
			record := approvalForReservation(state, state.TerminalCalls[0].ReservationIdentity)
			record.Status = actionapproval.StatusExpired
			clearReceipt(record)
			state.TerminalCalls = []TerminalCall{}
		}},
		{name: "failed approval retains active reservation", mutate: func(state *State) {
			record := approvalForReservation(state, state.Reservations[0].Identity)
			record.Status = actionapproval.StatusRejected
		}},
		{name: "duplicate terminal reservation identity", mutate: func(state *State) {
			duplicate := state.TerminalCalls[0]
			duplicate.CallID = callID("z")
			state.TerminalCalls = append(state.TerminalCalls, duplicate)
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			mutated := cloneState(baseline)
			test.mutate(&mutated)
			digest, err := fixture.store.stateDigest(mutated)
			if err != nil {
				t.Fatal(err)
			}
			mutated.Digest = digest
			if err := fixture.store.validateState(mutated, true); err == nil {
				t.Fatal("semantically malformed state with a fresh HMAC digest was accepted")
			}
		})
	}
}

func TestTransactionDecoderRejectsMalformedMetadataWithFreshDigest(t *testing.T) {
	fixture, previous := completePersistedState(t)
	next := cloneState(previous)
	next.Revision++
	next.LastObservedUnixNano++
	var err error
	next.Digest, err = fixture.store.stateDigest(next)
	if err != nil {
		t.Fatal(err)
	}
	baseline, err := fixture.store.newTransaction(previous, true, next)
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name     string
		mutate   func(*stateTransaction)
		redigest bool
	}{
		{name: "schema", redigest: true, mutate: func(transaction *stateTransaction) { transaction.Schema = "other" }},
		{name: "format version", redigest: true, mutate: func(transaction *stateTransaction) { transaction.FormatVersion = "2" }},
		{name: "before persistence", redigest: true, mutate: func(transaction *stateTransaction) { transaction.BeforePersisted = false }},
		{name: "before revision", redigest: true, mutate: func(transaction *stateTransaction) { transaction.BeforeRevision++ }},
		{name: "before key generation", redigest: true, mutate: func(transaction *stateTransaction) {
			transaction.BeforeDigest = "hmac-sha256:v1:" + strings.Repeat("8", 32) + ":" + strings.Repeat("8", 64)
		}},
		{name: "after revision", redigest: true, mutate: func(transaction *stateTransaction) { transaction.After.Revision++ }},
		{name: "after equals before", redigest: true, mutate: func(transaction *stateTransaction) { transaction.After.Digest = transaction.BeforeDigest }},
		{name: "transaction digest", mutate: func(transaction *stateTransaction) {
			transaction.Digest = fixture.key.Identity(DomainTransaction, []byte("other"))
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			mutated := baseline
			test.mutate(&mutated)
			if test.redigest {
				var digestErr error
				mutated.Digest, digestErr = fixture.store.transactionDigest(mutated)
				if digestErr != nil {
					t.Fatal(digestErr)
				}
			}
			body, marshalErr := json.Marshal(mutated)
			if marshalErr != nil {
				t.Fatal(marshalErr)
			}
			if _, decodeErr := fixture.store.decodeTransaction(body); decodeErr == nil {
				t.Fatal("malformed transaction with a fresh HMAC digest was accepted")
			}
		})
	}
}
