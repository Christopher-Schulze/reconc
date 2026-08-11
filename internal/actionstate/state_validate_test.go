package actionstate

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"reconc.dev/reconc/internal/action"
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
	_, first := fixture.reserve(t, callID("c"))
	version, err := fixture.store.ReserveApproval(
		context.Background(), first.Reservation.Identity, first.Snapshot.StateVersion,
	)
	if err != nil {
		t.Fatal(err)
	}
	version, err = fixture.store.CommitApproval(context.Background(), first.Reservation.Identity, version)
	if err != nil {
		t.Fatal(err)
	}
	version, err = fixture.store.MarkDispatched(context.Background(), first.Reservation.Identity, version)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.store.Settle(
		context.Background(), first.Reservation.Identity, version, OutcomeSucceeded, 37,
	); err != nil {
		t.Fatal(err)
	}
	_, second := fixture.reserve(t, callID("d"))
	version, err = fixture.store.ReserveApproval(
		context.Background(), second.Reservation.Identity, second.Snapshot.StateVersion,
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.store.CommitApproval(context.Background(), second.Reservation.Identity, version); err != nil {
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
		{name: "charge approval", mutate: func(state *State) { state.Reservations[0].Charges[0].ApprovalCommitted = false }},
		{name: "terminal call", mutate: func(state *State) { state.TerminalCalls[0].CallID = callID("f") }},
		{name: "terminal reservation", mutate: func(state *State) { state.TerminalCalls[0].ReservationIdentity = keyed("terminal") }},
		{name: "terminal outcome", mutate: func(state *State) { state.TerminalCalls[0].Outcome = OutcomeFailed }},
		{name: "terminal time", mutate: func(state *State) { state.TerminalCalls[0].CompletedAtUnix++ }},
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
	tests := []struct {
		name   string
		mutate func(*State)
	}{
		{name: "zero revision", mutate: func(state *State) { state.Revision = 0 }},
		{name: "missing clock", mutate: func(state *State) { state.ClockSource = "" }},
		{name: "nil budgets", mutate: func(state *State) { state.Budgets = nil }},
		{name: "nil reservations", mutate: func(state *State) { state.Reservations = nil }},
		{name: "nil terminal calls", mutate: func(state *State) { state.TerminalCalls = nil }},
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
