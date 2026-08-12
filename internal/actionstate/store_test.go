package actionstate

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"reconc.dev/reconc/internal/action"
	"reconc.dev/reconc/internal/actionapproval"
	"reconc.dev/reconc/internal/retention"
)

const (
	storePolicyDigest     = "1111111111111111111111111111111111111111111111111111111111111111"
	storeNextPolicyDigest = "2222222222222222222222222222222222222222222222222222222222222222"
	storeLockDigest       = "3333333333333333333333333333333333333333333333333333333333333333"
	storeToolDigest       = "sha256:4444444444444444444444444444444444444444444444444444444444444444"
	storeNextToolDigest   = "sha256:5555555555555555555555555555555555555555555555555555555555555555"
	storeExecutableDigest = "sha256:6666666666666666666666666666666666666666666666666666666666666666"
	storeNextExecutable   = "sha256:7777777777777777777777777777777777777777777777777777777777777777"
)

type controlledClock struct {
	mu       sync.Mutex
	snapshot ClockSnapshot
	err      error
}

func (c *controlledClock) Snapshot() (ClockSnapshot, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.snapshot, c.err
}

func (c *controlledClock) set(now time.Time, source string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.snapshot = ClockSnapshot{Time: now, Source: source}
	c.err = nil
}

type storeFixture struct {
	home              string
	repository        string
	key               *IdentityKey
	lease             *IdentityKeyLease
	store             *Store
	clock             *controlledClock
	context           BoundContext
	server            ObservedServer
	serverFingerprint string
	plan              *action.CompiledPlan
	authority         PolicyAuthority
	request           action.Request
}

func newStoreFixture(t testing.TB, budgets []action.Budget) *storeFixture {
	t.Helper()
	home := privateTestHome(t)
	repository := t.TempDir()
	now := time.Date(2026, 8, 11, 12, 0, 0, 123, time.UTC)
	key, err := createIdentityKey(home, now, strings.NewReader(strings.Repeat("s", 32)))
	if err != nil {
		t.Fatal(err)
	}
	lease, err := AcquireIdentityKey(context.Background(), home)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := lease.Close(); err != nil {
			t.Errorf("close identity-key lease: %v", err)
		}
	})
	credential, err := CredentialIdentity(key, "warehouse", []byte("credential-secret"))
	if err != nil {
		t.Fatal(err)
	}
	bound, err := (OperatorContext{
		Principal: "release-operator", Role: "operator", Environment: "production",
		Credentials: []CredentialBinding{{Label: "warehouse", Identity: credential}},
		ServerLabel: "server", RunID: "Run:42", SessionID: "session_7",
	}).Bind(key)
	if err != nil {
		t.Fatal(err)
	}
	repositoryPath, repositoryIdentity, err := ObserveRepository(key, repository)
	if err != nil {
		t.Fatal(err)
	}
	server := testObservedServer(key, storeExecutableDigest, "server-fixture")
	serverFingerprint := server.ServerIdentity
	plan := compileStorePlan(t, serverFingerprint, budgets)
	clock := &controlledClock{snapshot: ClockSnapshot{Time: now, Source: "test-clock"}}
	store, err := OpenStore(StoreOptions{
		Home: home, Repository: repositoryPath, KeyLease: lease,
		Clock: clock, OwnerID: "owner-primary",
	})
	if err != nil {
		t.Fatal(err)
	}
	arguments, err := action.ParseJSON([]byte(`{"amount":1,"target":"staging"}`))
	if err != nil {
		t.Fatal(err)
	}
	request := action.Request{
		FormatVersion: action.RequestFormatVersion,
		Transport:     action.TransportMCPStdio, ServerLabel: "server",
		ServerFingerprint: serverFingerprint, Tool: "execute",
		ToolContractDigest: storeToolDigest, Phase: action.PhasePreCall,
		RepositoryIdentity: repositoryIdentity, PolicyDigest: storePolicyDigest,
		LockDigest: storeLockDigest, AuthorityMode: action.AuthorityOperatorPinned,
		Arguments: &arguments, Context: []action.ContextValue{},
		Completeness: action.CompleteEvidence(), Deadline: action.DeadlineReady,
	}
	return &storeFixture{
		home: home, repository: repositoryPath, key: key, lease: lease,
		store: store, clock: clock, context: bound, server: server,
		serverFingerprint: serverFingerprint, plan: plan, request: request,
		authority: PolicyAuthority{Mode: action.AuthorityOperatorPinned, ExpectedLockDigest: storeLockDigest},
	}
}

func compileStorePlan(t testing.TB, fingerprint string, budgets []action.Budget) *action.CompiledPlan {
	t.Helper()
	cost := uint64(2)
	tool := action.Tool{
		ID: "database-write", Transport: action.TransportMCPStdio,
		ServerLabel: "server", ServerFingerprint: fingerprint, Tool: "execute",
		Effect: action.Effect{Kind: action.EffectExternal}, CostUnits: &cost,
		MaxResultBytes: 100, Origin: action.OriginActions, SourceIdentity: ".reconc.yml",
	}
	compiled, err := action.CompilePlan(action.Plan{
		Tools: []action.Tool{tool}, Budgets: budgets,
		Rules: []action.Rule{{
			ID: "approve-database-write", Selector: action.Selector{ToolIDs: []string{"database-write"}},
			Decision: action.DecisionRequireApproval, OnIndeterminate: action.DecisionBlock,
			Cache: action.CacheExact, SourceIdentity: ".reconc.yml",
		}},
		Approvals: []action.ApprovalDisclosure{{
			ID: "database-write-summary", Selector: action.Selector{ToolIDs: []string{"database-write"}},
			SelectedArguments: []string{"/target"}, SourceIdentity: ".reconc.yml",
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return compiled
}

func storeBudget(id string, limits action.BudgetLimits, reset action.BudgetReset) action.Budget {
	budget := action.Budget{
		ID: id, Selector: action.Selector{ToolIDs: []string{"database-write"}},
		Limits: limits, Reset: reset, OnExhaustion: action.DecisionBlock,
		SourceIdentity: ".reconc.yml",
	}
	if reset == action.BudgetResetFixedWindow {
		budget.WindowSeconds = 60
	}
	return budget
}

func (f *storeFixture) reserve(t testing.TB, callID string) (ReserveRequest, ReserveResult) {
	t.Helper()
	version, err := f.store.CurrentStateVersion(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	request := f.request
	request.CallID = callID
	request.StateVersion = version
	input := ReserveRequest{
		Plan: f.plan, Request: request, Context: f.context, Authority: f.authority,
		Server: f.server,
	}
	result, err := f.store.Reserve(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	return input, result
}

func testObservedServer(key *IdentityKey, executableDigest, marker string) ObservedServer {
	server := ObservedServer{
		ExecutableDigest: executableDigest,
		ArgvIdentity:     key.Identity(DomainArgv, []byte(marker)),
		WorkingDirIdentity: key.Identity(
			DomainWorkingDirectory, []byte("working-directory"),
		),
		EnvironmentNames:    []string{},
		Environment:         []ObservedEnvironment{},
		EnvironmentIdentity: key.Identity(DomainEnvironmentName),
	}
	server.ServerIdentity = observedServerIdentity(key, server)
	return server
}

func callID(character string) string {
	return "act_" + strings.Repeat(character, 26)
}

func requireStateCode(t testing.TB, err error, code action.ReasonCode) {
	t.Helper()
	var stateErr *StateError
	if !errors.As(err, &stateErr) || stateErr.Code != code {
		t.Fatalf("state error = %v, want %s", err, code)
	}
}

func TestBudgetStoreCommitsEveryDispatchAndApprovalDimension(t *testing.T) {
	limits := action.BudgetLimits{
		CallCount: 2, DeniedCount: 2, ApprovalCount: 2, ArgumentBytes: 4096,
		ResultBytes: 1000, CostUnits: 4, Concurrent: 2,
	}
	fixture := newStoreFixture(t, []action.Budget{storeBudget("production", limits, action.BudgetResetNever)})
	input, reserved := fixture.reserve(t, callID("a"))
	if reserved.Reservation == nil || len(reserved.Snapshot.Candidates) != 1 {
		t.Fatalf("reservation = %#v", reserved)
	}
	candidate := reserved.Snapshot.Candidates[0]
	argumentBody, err := input.Request.Arguments.MarshalJSON()
	if err != nil {
		t.Fatal(err)
	}
	wantRequired := action.BudgetUsage{
		CallCount: 1, ArgumentBytes: uint64(len(argumentBody)), ResultBytes: 100,
		CostUnits: 2, Concurrent: 1,
	}
	if candidate.Required != wantRequired || !candidate.ReservationApplied || !candidate.Available {
		t.Fatalf("reserved candidate = %#v, want required %#v", candidate, wantRequired)
	}

	issued := issueFixtureApproval(t, fixture, input, reserved)
	approval, err := consumeFixtureApproval(t, fixture, issued, actionapproval.DecisionApprove)
	if err != nil {
		t.Fatal(err)
	}
	version := approval.StateVersion
	version, err = fixture.store.MarkDispatched(context.Background(), reserved.Reservation.Identity, version)
	if err != nil {
		t.Fatal(err)
	}
	version, err = fixture.store.Settle(
		context.Background(), reserved.Reservation.Identity, version, OutcomeSucceeded, 42,
	)
	if err != nil {
		t.Fatal(err)
	}
	if version == reserved.Snapshot.StateVersion {
		t.Fatal("terminal settlement did not advance state")
	}
	status, err := fixture.store.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	wantConsumed := action.BudgetUsage{
		CallCount: 1, ApprovalCount: 1, ArgumentBytes: uint64(len(argumentBody)),
		ResultBytes: 42, CostUnits: 2,
	}
	if len(status.Budgets) != 1 || status.Budgets[0].Consumed != wantConsumed ||
		status.Budgets[0].Reserved != (action.BudgetUsage{}) || status.LiveReservations != 0 ||
		status.TerminalCallCount != 1 || !status.Complete {
		t.Fatalf("terminal status = %#v, want consumed %#v", status, wantConsumed)
	}
	if _, err := fixture.store.Settle(
		context.Background(), reserved.Reservation.Identity, version, OutcomeSucceeded, 42,
	); err == nil {
		t.Fatal("duplicate settlement was accepted")
	} else {
		requireStateCode(t, err, action.ReasonReservationIndeterminate)
	}
}

func TestBudgetStoreReturnsCanonicalAbsentSnapshotWithoutMutation(t *testing.T) {
	fixture := newStoreFixture(t, nil)
	input, result := fixture.reserve(t, callID("r"))
	if result.Reservation != nil || result.Snapshot.StateVersion != input.Request.StateVersion ||
		result.Snapshot.Identity != "absent" || result.Snapshot.ReservationIdentity != "absent" ||
		!result.Snapshot.Complete || result.Snapshot.Candidates == nil || len(result.Snapshot.Candidates) != 0 {
		t.Fatalf("unbudgeted snapshot = %#v", result)
	}
	status, err := fixture.store.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if status.Revision != 0 || len(status.Budgets) != 0 || status.LiveReservations != 0 {
		t.Fatalf("unbudgeted call mutated state = %#v", status)
	}
}

func TestBudgetReservationRetryRequiresExactTrustedCallIdentity(t *testing.T) {
	fixture := newStoreFixture(t, []action.Budget{storeBudget(
		"retry", action.BudgetLimits{CallCount: 2, ArgumentBytes: 4096}, action.BudgetResetNever,
	)})
	input, first := fixture.reserve(t, callID("s"))
	input.Request.StateVersion = first.Snapshot.StateVersion
	retried, err := fixture.store.Reserve(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if retried.Reservation == nil || retried.Reservation.Identity != first.Reservation.Identity ||
		retried.Snapshot.StateVersion != first.Snapshot.StateVersion {
		t.Fatalf("idempotent retry = %#v, first=%#v", retried, first)
	}
	mutated, err := action.ParseJSON([]byte(`{"amount":2,"target":"staging"}`))
	if err != nil {
		t.Fatal(err)
	}
	input.Request.Arguments = &mutated
	if _, err := fixture.store.Reserve(context.Background(), input); err == nil {
		t.Fatal("same call ID with changed arguments reused a reservation")
	} else {
		requireStateCode(t, err, action.ReasonReservationIndeterminate)
	}
}

func TestBudgetStoreRejectsEveryTrustedContextAndAuthoritySpoof(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*ReserveRequest)
		code   action.ReasonCode
	}{
		{
			name:   "principal with stale identity",
			mutate: func(input *ReserveRequest) { input.Context.Principal = "other-operator" },
			code:   action.ReasonIdentityUnavailable,
		},
		{
			name:   "role with stale identity",
			mutate: func(input *ReserveRequest) { input.Context.Role = "auditor" },
			code:   action.ReasonIdentityUnavailable,
		},
		{
			name:   "environment with stale identity",
			mutate: func(input *ReserveRequest) { input.Context.Environment = "staging" },
			code:   action.ReasonIdentityUnavailable,
		},
		{
			name:   "credential label with stale identity",
			mutate: func(input *ReserveRequest) { input.Context.Credentials[0].Label = "other" },
			code:   action.ReasonIdentityUnavailable,
		},
		{
			name:   "untrusted provenance",
			mutate: func(input *ReserveRequest) { input.Context.Provenance = action.ProvenanceAgentSupplied },
			code:   action.ReasonIdentityUnavailable,
		},
		{
			name:   "missing authority",
			mutate: func(input *ReserveRequest) { input.Authority = PolicyAuthority{} },
			code:   action.ReasonAuthorityUnavailable,
		},
		{
			name:   "wrong pinned digest",
			mutate: func(input *ReserveRequest) { input.Authority.ExpectedLockDigest = storePolicyDigest },
			code:   action.ReasonLockMismatch,
		},
		{
			name: "authority mode mismatch",
			mutate: func(input *ReserveRequest) {
				input.Authority = PolicyAuthority{Mode: action.AuthorityRepositoryManaged}
			},
			code: action.ReasonAuthorityUnavailable,
		},
		{
			name: "unkeyed server",
			mutate: func(input *ReserveRequest) {
				input.Request.ServerFingerprint = "sha256:" + strings.Repeat("a", 64)
			},
			code: action.ReasonInvalidRequest,
		},
		{
			name: "changed repository",
			mutate: func(input *ReserveRequest) {
				input.Request.RepositoryIdentity = input.Context.ContextIdentity
			},
			code: action.ReasonIdentityUnavailable,
		},
		{
			name: "server observation mismatch",
			mutate: func(input *ReserveRequest) {
				input.Server.ExecutableDigest = storeNextExecutable
			},
			code: action.ReasonIdentityUnavailable,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newStoreFixture(t, []action.Budget{storeBudget(
				"spoof", action.BudgetLimits{CallCount: 1}, action.BudgetResetNever,
			)})
			version, err := fixture.store.CurrentStateVersion(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			request := fixture.request
			request.CallID, request.StateVersion = callID("x"), version
			input := ReserveRequest{
				Plan: fixture.plan, Request: request, Context: fixture.context,
				Authority: fixture.authority, Server: fixture.server,
			}
			input.Context.Credentials = append([]CredentialBinding(nil), fixture.context.Credentials...)
			test.mutate(&input)
			_, err = fixture.store.Reserve(context.Background(), input)
			if err == nil {
				t.Fatal("spoofed trusted input was accepted")
			}
			requireStateCode(t, err, test.code)
		})
	}
}

func TestBudgetStoreRecordsDenialsAndExhaustsApprovalAtomically(t *testing.T) {
	limits := action.BudgetLimits{CallCount: 10, DeniedCount: 1, ApprovalCount: 1}
	fixture := newStoreFixture(t, []action.Budget{storeBudget("events", limits, action.BudgetResetNever)})
	_, first := fixture.reserve(t, callID("b"))
	if _, err := fixture.store.RecordDenied(
		context.Background(), first.Reservation.Identity, first.Snapshot.StateVersion,
	); err != nil {
		t.Fatal(err)
	}
	_, second := fixture.reserve(t, callID("c"))
	version, err := fixture.store.RecordDenied(
		context.Background(), second.Reservation.Identity, second.Snapshot.StateVersion,
	)
	requireStateCode(t, err, action.ReasonBudgetExhausted)
	if version == "" {
		t.Fatal("exhausted denial did not persist terminal cleanup")
	}

	_, approved := fixture.reserve(t, callID("d"))
	approvedInput := fixture.request
	approvedInput.CallID = callID("d")
	approvedInput.StateVersion = approved.Snapshot.StateVersion
	issued := issueFixtureApproval(t, fixture, ReserveRequest{
		Plan: fixture.plan, Request: approvedInput, Context: fixture.context,
		Authority: fixture.authority, Server: fixture.server,
	}, approved)
	approval, err := consumeFixtureApproval(t, fixture, issued, actionapproval.DecisionApprove)
	if err != nil {
		t.Fatal(err)
	}
	version = approval.StateVersion
	if _, err := fixture.store.Release(context.Background(), approved.Reservation.Identity, version); err != nil {
		t.Fatal(err)
	}
	_, exhausted := fixture.reserve(t, callID("e"))
	exhaustedRequest := fixture.request
	exhaustedRequest.CallID = callID("e")
	exhaustedRequest.StateVersion = exhausted.Snapshot.StateVersion
	if _, err := fixture.store.IssueApproval(context.Background(), ApprovalIssueRequest{
		Binding: ApprovalBinding{
			Plan: fixture.plan, Context: fixture.context, Authority: fixture.authority, Server: fixture.server,
			Evaluation: action.EvaluationInput{
				Request: exhaustedRequest, SourceIdentity: strings.Repeat("8", 64),
				ContextIdentity: fixture.context.ContextIdentity, ExecutableDigest: fixture.server.ExecutableDigest,
				Principal: fixture.context.Principal, CredentialLabels: credentialLabels(fixture.context.Credentials),
				Budget:    exhausted.Snapshot,
				Approval:  action.ApprovalSnapshot{Status: action.ApprovalNone, Identity: "approval-none"},
				Taint:     action.TaintSnapshot{Status: action.TaintClean, Identity: "taint-clean"},
				Lifecycle: action.LifecycleActive, CachePolicyVersion: action.CacheIdentityVersion,
			},
		},
		AuthorityPolicyID: "production-writes", TTL: 30 * time.Second,
	}); err == nil {
		t.Fatal("exhausted approval capacity was reserved")
	} else {
		requireStateCode(t, err, action.ReasonBudgetExhausted)
	}
	if _, err := fixture.store.Release(
		context.Background(), exhausted.Reservation.Identity, exhausted.Snapshot.StateVersion,
	); err != nil {
		t.Fatal(err)
	}
	status, err := fixture.store.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got := status.Budgets[0].Consumed; got.DeniedCount != 1 || got.ApprovalCount != 1 ||
		status.LiveReservations != 0 || status.TerminalCallCount != 4 {
		t.Fatalf("event status = %#v", status)
	}
}

func TestBudgetStoreWithholdsOversizedResultUntilExplicitReconciliation(t *testing.T) {
	limits := action.BudgetLimits{CallCount: 2, ResultBytes: 1000, Concurrent: 2}
	fixture := newStoreFixture(t, []action.Budget{storeBudget("results", limits, action.BudgetResetNever)})
	_, reserved := fixture.reserve(t, callID("f"))
	version, err := fixture.store.MarkDispatched(
		context.Background(), reserved.Reservation.Identity, reserved.Snapshot.StateVersion,
	)
	if err != nil {
		t.Fatal(err)
	}
	version, err = fixture.store.Settle(
		context.Background(), reserved.Reservation.Identity, version, OutcomeSucceeded, 101,
	)
	requireStateCode(t, err, action.ReasonResultWithheld)
	status, statusErr := fixture.store.Status(context.Background())
	if statusErr != nil {
		t.Fatal(statusErr)
	}
	if version == "" || status.Indeterminate != 1 || status.LiveReservations != 1 {
		t.Fatalf("indeterminate status = %#v, version=%q", status, version)
	}
	authorization, err := NewReconciliationAuthorization(
		fixture.key, reserved.Reservation.Identity, version, OutcomeIndeterminateCommitted, 0,
	)
	if err != nil {
		t.Fatal(err)
	}
	wrongAuthorization, err := NewReconciliationAuthorization(
		fixture.key, reserved.Reservation.Identity, version, OutcomeFailed, 0,
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.store.ReconcileIndeterminate(
		context.Background(), reserved.Reservation.Identity, version,
		Reconciliation{AuthorizationIdentity: wrongAuthorization, Outcome: OutcomeIndeterminateCommitted},
	); err == nil {
		t.Fatal("reconciliation authorization for a different outcome was accepted")
	} else {
		requireStateCode(t, err, action.ReasonAuthorityUnavailable)
	}
	_, err = fixture.store.ReconcileIndeterminate(
		context.Background(), reserved.Reservation.Identity, version,
		Reconciliation{AuthorizationIdentity: authorization, Outcome: OutcomeIndeterminateCommitted},
	)
	if err != nil {
		t.Fatal(err)
	}
	status, err = fixture.store.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if status.Indeterminate != 0 || status.LiveReservations != 0 ||
		status.Budgets[0].Consumed.ResultBytes != 100 || status.TerminalCallCount != 1 {
		t.Fatalf("reconciled status = %#v", status)
	}
}

func TestBudgetStoreConservativelyMarksUnknownAndAbandonedCalls(t *testing.T) {
	limits := action.BudgetLimits{CallCount: 4, ResultBytes: 400, Concurrent: 4}
	fixture := newStoreFixture(t, []action.Budget{storeBudget("unknown", limits, action.BudgetResetNever)})
	_, first := fixture.reserve(t, callID("t"))
	version, err := fixture.store.MarkIndeterminate(
		context.Background(), first.Reservation.Identity, first.Snapshot.StateVersion,
	)
	if err != nil {
		t.Fatal(err)
	}
	if repeated, err := fixture.store.MarkIndeterminate(
		context.Background(), first.Reservation.Identity, version,
	); err != nil || repeated != version {
		t.Fatalf("idempotent indeterminate transition = %q, %v", repeated, err)
	}

	_, second := fixture.reserve(t, callID("u"))
	authorization, err := NewOwnerAbandonmentAuthorization(fixture.key, "owner-primary", second.Snapshot.StateVersion)
	if err != nil {
		t.Fatal(err)
	}
	version, err = fixture.store.MarkOwnerAbandoned(
		context.Background(), "owner-primary", second.Snapshot.StateVersion, authorization,
	)
	if err != nil {
		t.Fatal(err)
	}
	status, err := fixture.store.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if status.Indeterminate != 2 || status.LiveReservations != 2 ||
		status.Budgets[0].Consumed.CallCount != 2 || status.Budgets[0].Reserved.Concurrent != 2 {
		t.Fatalf("conservative unknown-call status = %#v", status)
	}
	missingAuthorization, err := NewOwnerAbandonmentAuthorization(fixture.key, "missing-owner", version)
	if err != nil {
		t.Fatal(err)
	}
	if unchanged, err := fixture.store.MarkOwnerAbandoned(
		context.Background(), "missing-owner", version, missingAuthorization,
	); err != nil || unchanged != version {
		t.Fatalf("empty owner reconciliation = %q, %v", unchanged, err)
	}
}

func TestBudgetStoreUsesTrustedWindowsAndRejectsClockDrift(t *testing.T) {
	limits := action.BudgetLimits{RateWindow: 1}
	fixture := newStoreFixture(t, []action.Budget{storeBudget("minute", limits, action.BudgetResetFixedWindow)})
	_, first := fixture.reserve(t, callID("g"))
	version, err := fixture.store.MarkDispatched(
		context.Background(), first.Reservation.Identity, first.Snapshot.StateVersion,
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.store.Settle(
		context.Background(), first.Reservation.Identity, version, OutcomeSucceeded, 0,
	); err != nil {
		t.Fatal(err)
	}
	_, exhausted := fixture.reserve(t, callID("h"))
	if exhausted.Reservation != nil || exhausted.Snapshot.Candidates[0].Available ||
		exhausted.Snapshot.Candidates[0].Reason != action.ReasonBudgetExhausted {
		t.Fatalf("same-window capacity = %#v", exhausted)
	}
	fixture.clock.set(time.Date(2026, 8, 11, 12, 1, 0, 0, time.UTC), "test-clock")
	_, nextWindow := fixture.reserve(t, callID("i"))
	if nextWindow.Reservation == nil {
		t.Fatal("next trusted window did not receive fresh scoped capacity")
	}
	if _, err := fixture.store.Release(
		context.Background(), nextWindow.Reservation.Identity, nextWindow.Snapshot.StateVersion,
	); err != nil {
		t.Fatal(err)
	}
	fixture.clock.set(time.Date(2026, 8, 11, 12, 0, 30, 0, time.UTC), "test-clock")
	version, err = fixture.store.CurrentStateVersion(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	request := fixture.request
	request.CallID, request.StateVersion = callID("j"), version
	if _, err := fixture.store.Reserve(context.Background(), ReserveRequest{
		Plan: fixture.plan, Request: request, Context: fixture.context, Authority: fixture.authority,
		Server: fixture.server,
	}); err == nil {
		t.Fatal("trusted clock rollback was accepted")
	} else {
		requireStateCode(t, err, action.ReasonStateUnavailable)
	}
	fixture.clock.set(time.Date(2026, 8, 11, 12, 2, 0, 0, time.UTC), "changed-clock")
	if _, err := fixture.store.Reserve(context.Background(), ReserveRequest{
		Plan: fixture.plan, Request: request, Context: fixture.context, Authority: fixture.authority,
		Server: fixture.server,
	}); err == nil {
		t.Fatal("trusted clock source discontinuity was accepted")
	} else {
		requireStateCode(t, err, action.ReasonStateUnavailable)
	}
}

func TestBudgetStoreRefusesDispatchAfterReservationWindowExpires(t *testing.T) {
	fixture := newStoreFixture(t, []action.Budget{storeBudget(
		"dispatch-window", action.BudgetLimits{RateWindow: 2}, action.BudgetResetFixedWindow,
	)})
	_, reserved := fixture.reserve(t, callID("w"))
	fixture.clock.set(time.Date(2026, 8, 11, 12, 1, 0, 0, time.UTC), "test-clock")

	if _, err := fixture.store.MarkDispatched(
		context.Background(), reserved.Reservation.Identity, reserved.Snapshot.StateVersion,
	); err == nil {
		t.Fatal("expired fixed-window reservation was dispatched")
	} else {
		requireStateCode(t, err, action.ReasonStateUnavailable)
	}
	version, err := fixture.store.CurrentStateVersion(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.store.Release(
		context.Background(), reserved.Reservation.Identity, version,
	); err != nil {
		t.Fatalf("release expired pre-dispatch reservation: %v", err)
	}
}

func TestBudgetStorePersistsRejectedForwardClockBeforeRollback(t *testing.T) {
	fixture := newStoreFixture(t, []action.Budget{storeBudget(
		"rollback-window", action.BudgetLimits{RateWindow: 2}, action.BudgetResetFixedWindow,
	)})
	_, reserved := fixture.reserve(t, callID("2"))
	fixture.clock.set(time.Date(2026, 8, 11, 12, 1, 0, 0, time.UTC), "test-clock")
	if _, err := fixture.store.MarkDispatched(
		context.Background(), reserved.Reservation.Identity, reserved.Snapshot.StateVersion,
	); err == nil {
		t.Fatal("expired reservation was dispatched")
	} else {
		requireStateCode(t, err, action.ReasonStateUnavailable)
	}
	advancedVersion, err := fixture.store.CurrentStateVersion(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if advancedVersion == reserved.Snapshot.StateVersion {
		t.Fatal("rejected forward clock observation was not persisted")
	}

	fixture.clock.set(time.Date(2026, 8, 11, 12, 0, 30, 0, time.UTC), "test-clock")
	if _, err := fixture.store.MarkDispatched(
		context.Background(), reserved.Reservation.Identity, advancedVersion,
	); err == nil {
		t.Fatal("clock rollback revived an expired reservation")
	} else {
		requireStateCode(t, err, action.ReasonStateUnavailable)
	}
}

func TestBudgetStorePrunesExpiredFixedWindowsWithoutReturningCurrentCapacity(t *testing.T) {
	fixture := newStoreFixture(t, []action.Budget{storeBudget(
		"bounded-windows", action.BudgetLimits{RateWindow: 1}, action.BudgetResetFixedWindow,
	)})
	for index := 0; index < 12; index++ {
		fixture.clock.set(time.Date(2026, 8, 11, 12, index, 0, 0, time.UTC), "test-clock")
		id, err := NewRandomCallID()
		if err != nil {
			t.Fatal(err)
		}
		_, reserved := fixture.reserve(t, id)
		version, err := fixture.store.MarkDispatched(
			context.Background(), reserved.Reservation.Identity, reserved.Snapshot.StateVersion,
		)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := fixture.store.Settle(
			context.Background(), reserved.Reservation.Identity, version, OutcomeSucceeded, 0,
		); err != nil {
			t.Fatal(err)
		}
	}
	status, err := fixture.store.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(status.Budgets) > 2 || status.Budgets[len(status.Budgets)-1].Consumed.RateWindow != 1 {
		t.Fatalf("bounded fixed-window state = %#v", status.Budgets)
	}
}

func TestBudgetStoreCarriesConsumptionAcrossGenerationAndBlocksStaleReservation(t *testing.T) {
	limits := action.BudgetLimits{CallCount: 3}
	fixture := newStoreFixture(t, []action.Budget{storeBudget("generation", limits, action.BudgetResetNever)})
	_, first := fixture.reserve(t, callID("k"))
	version, err := fixture.store.MarkDispatched(
		context.Background(), first.Reservation.Identity, first.Snapshot.StateVersion,
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.store.Settle(
		context.Background(), first.Reservation.Identity, version, OutcomeSucceeded, 0,
	); err != nil {
		t.Fatal(err)
	}
	_, stale := fixture.reserve(t, callID("l"))

	current, err := fixture.store.CurrentStateVersion(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	request := fixture.request
	request.CallID, request.StateVersion = callID("m"), current
	request.PolicyDigest = storeNextPolicyDigest
	request.ToolContractDigest = storeNextToolDigest
	nextServer := testObservedServer(fixture.key, storeNextExecutable, "next-server")
	request.ServerFingerprint = nextServer.ServerIdentity
	next := ReserveRequest{
		Plan: compileStorePlan(t, nextServer.ServerIdentity, []action.Budget{storeBudget(
			"generation", limits, action.BudgetResetNever,
		)}),
		Request: request, Context: fixture.context, Authority: fixture.authority, Server: nextServer,
	}
	rotated, err := fixture.store.Reserve(context.Background(), next)
	if err != nil {
		t.Fatal(err)
	}
	if rotated.Reservation == nil || rotated.Snapshot.Candidates[0].Consumed.CallCount != 1 {
		t.Fatalf("cross-generation reservation = %#v", rotated)
	}
	if _, err := fixture.store.MarkDispatched(
		context.Background(), stale.Reservation.Identity, rotated.Snapshot.StateVersion,
	); err == nil {
		t.Fatal("stale governing generation was dispatched")
	} else {
		requireStateCode(t, err, action.ReasonStateUnavailable)
	}
	version, err = fixture.store.Release(
		context.Background(), stale.Reservation.Identity, rotated.Snapshot.StateVersion,
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.store.Release(context.Background(), rotated.Reservation.Identity, version); err != nil {
		t.Fatal(err)
	}
	status, err := fixture.store.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if status.Budgets[0].Consumed.CallCount != 1 ||
		len(status.Budgets[0].GenerationHistory) != 2 ||
		status.Budgets[0].Generation.PolicyDigest != storeNextPolicyDigest {
		t.Fatalf("generation carry-forward status = %#v", status)
	}
	changedServer := testObservedServer(fixture.key, storeNextExecutable, "changed-server")
	newFingerprint := changedServer.ServerIdentity
	current, err = fixture.store.CurrentStateVersion(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	request.ServerFingerprint, request.StateVersion = newFingerprint, current
	request.CallID = callID("v")
	serverChanged, err := fixture.store.Reserve(context.Background(), ReserveRequest{
		Plan: compileStorePlan(t, newFingerprint, []action.Budget{storeBudget(
			"generation", limits, action.BudgetResetNever,
		)}),
		Request: request, Context: fixture.context, Server: changedServer, Authority: fixture.authority,
	})
	if err != nil {
		t.Fatal(err)
	}
	if serverChanged.Reservation == nil || serverChanged.Snapshot.Candidates[0].Consumed.CallCount != 1 ||
		serverChanged.Snapshot.Candidates[0].Scope.ServerIdentity != newFingerprint {
		t.Fatalf("server-identity carry-forward = %#v", serverChanged)
	}
}

func TestBudgetStorePersistsExhaustedGoverningGeneration(t *testing.T) {
	limits := action.BudgetLimits{CallCount: 1}
	fixture := newStoreFixture(t, []action.Budget{storeBudget("exhausted-generation", limits, action.BudgetResetNever)})
	_, first := fixture.reserve(t, callID("y"))
	version, err := fixture.store.MarkDispatched(
		context.Background(), first.Reservation.Identity, first.Snapshot.StateVersion,
	)
	if err != nil {
		t.Fatal(err)
	}
	version, err = fixture.store.Settle(
		context.Background(), first.Reservation.Identity, version, OutcomeSucceeded, 0,
	)
	if err != nil {
		t.Fatal(err)
	}

	request := fixture.request
	request.CallID, request.StateVersion = callID("z"), version
	request.PolicyDigest = storeNextPolicyDigest
	request.ToolContractDigest = storeNextToolDigest
	nextServer := testObservedServer(fixture.key, storeNextExecutable, "exhausted-generation")
	request.ServerFingerprint = nextServer.ServerIdentity
	result, err := fixture.store.Reserve(context.Background(), ReserveRequest{
		Plan: compileStorePlan(t, nextServer.ServerIdentity, []action.Budget{storeBudget(
			"exhausted-generation", limits, action.BudgetResetNever,
		)}),
		Request: request, Context: fixture.context, Authority: fixture.authority, Server: nextServer,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Reservation != nil || result.Snapshot.StateVersion == version ||
		len(result.Snapshot.Candidates) != 1 || result.Snapshot.Candidates[0].Available ||
		result.Snapshot.Candidates[0].Generation.PolicyDigest != storeNextPolicyDigest {
		t.Fatalf("exhausted generation result = %#v", result)
	}
	status, err := fixture.store.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if status.StateVersion != result.Snapshot.StateVersion || len(status.Budgets) != 1 ||
		len(status.Budgets[0].GenerationHistory) != 2 ||
		status.Budgets[0].Generation.PolicyDigest != storeNextPolicyDigest {
		t.Fatalf("persisted exhausted generation = %#v", status)
	}
}

func TestBudgetStoreCarriesConsumptionAcrossResetContractChange(t *testing.T) {
	limits := action.BudgetLimits{CallCount: 3}
	fixture := newStoreFixture(t, []action.Budget{storeBudget("reset-change", limits, action.BudgetResetNever)})
	_, first := fixture.reserve(t, callID("3"))
	version, err := fixture.store.MarkDispatched(
		context.Background(), first.Reservation.Identity, first.Snapshot.StateVersion,
	)
	if err != nil {
		t.Fatal(err)
	}
	version, err = fixture.store.Settle(
		context.Background(), first.Reservation.Identity, version, OutcomeSucceeded, 0,
	)
	if err != nil {
		t.Fatal(err)
	}

	changedBudget := storeBudget("reset-change", limits, action.BudgetResetOperatorRun)
	changedPlan := compileStorePlan(t, fixture.serverFingerprint, []action.Budget{changedBudget})
	request := fixture.request
	request.CallID, request.StateVersion, request.PolicyDigest = callID("4"), version, storeNextPolicyDigest
	changed, err := fixture.store.Reserve(context.Background(), ReserveRequest{
		Plan: changedPlan, Request: request, Context: fixture.context, Authority: fixture.authority,
		Server: fixture.server,
	})
	if err != nil {
		t.Fatal(err)
	}
	if changed.Reservation == nil || len(changed.Snapshot.Candidates) != 1 ||
		changed.Snapshot.Candidates[0].Consumed.CallCount != 1 {
		t.Fatalf("reset-contract carry-forward = %#v", changed)
	}
	if _, err := fixture.store.Release(
		context.Background(), changed.Reservation.Identity, changed.Snapshot.StateVersion,
	); err != nil {
		t.Fatal(err)
	}
}

func TestBudgetStoreBlocksResetContractChangeUntilPriorReservationIsResolved(t *testing.T) {
	limits := action.BudgetLimits{CallCount: 2}
	fixture := newStoreFixture(t, []action.Budget{storeBudget("reset-live", limits, action.BudgetResetNever)})
	_, live := fixture.reserve(t, callID("5"))

	changedBudget := storeBudget("reset-live", limits, action.BudgetResetOperatorRun)
	request := fixture.request
	request.CallID, request.StateVersion, request.PolicyDigest =
		callID("6"), live.Snapshot.StateVersion, storeNextPolicyDigest
	changedRequest := ReserveRequest{
		Plan:    compileStorePlan(t, fixture.serverFingerprint, []action.Budget{changedBudget}),
		Request: request, Context: fixture.context, Authority: fixture.authority,
		Server: fixture.server,
	}
	if _, err := fixture.store.Reserve(context.Background(), changedRequest); err == nil {
		t.Fatal("reset-contract change bypassed a live prior reservation")
	} else {
		requireStateCode(t, err, action.ReasonReservationIndeterminate)
	}

	version, err := fixture.store.Release(
		context.Background(), live.Reservation.Identity, live.Snapshot.StateVersion,
	)
	if err != nil {
		t.Fatal(err)
	}
	changedRequest.Request.StateVersion = version
	changed, err := fixture.store.Reserve(context.Background(), changedRequest)
	if err != nil {
		t.Fatal(err)
	}
	if changed.Reservation == nil || changed.Snapshot.Candidates[0].Consumed.CallCount != 0 {
		t.Fatalf("resolved reset-contract change = %#v", changed)
	}
}

func TestBudgetStoreBlocksLimitChangeThatWouldInvalidateLiveReservation(t *testing.T) {
	limits := action.BudgetLimits{CallCount: 2, ResultBytes: 100}
	fixture := newStoreFixture(t, []action.Budget{storeBudget("limit-live", limits, action.BudgetResetNever)})
	_, live := fixture.reserve(t, callID("7"))

	changedBudget := storeBudget("limit-live", action.BudgetLimits{CallCount: 2}, action.BudgetResetNever)
	request := fixture.request
	request.CallID, request.StateVersion, request.PolicyDigest =
		callID("b"), live.Snapshot.StateVersion, storeNextPolicyDigest
	changedRequest := ReserveRequest{
		Plan:    compileStorePlan(t, fixture.serverFingerprint, []action.Budget{changedBudget}),
		Request: request, Context: fixture.context, Authority: fixture.authority,
		Server: fixture.server,
	}
	if _, err := fixture.store.Reserve(context.Background(), changedRequest); err == nil {
		t.Fatal("limit change invalidated a live reservation")
	} else {
		requireStateCode(t, err, action.ReasonReservationIndeterminate)
	}
	version, err := fixture.store.CurrentStateVersion(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if version != live.Snapshot.StateVersion {
		t.Fatal("rejected limit change mutated durable state")
	}
	version, err = fixture.store.Release(context.Background(), live.Reservation.Identity, version)
	if err != nil {
		t.Fatal(err)
	}
	changedRequest.Request.StateVersion = version
	changed, err := fixture.store.Reserve(context.Background(), changedRequest)
	if err != nil {
		t.Fatal(err)
	}
	if changed.Reservation == nil || changed.Snapshot.Candidates[0].Required.ResultBytes != 0 {
		t.Fatalf("resolved limit change = %#v", changed)
	}
}

func TestBudgetStoreRejectsLimitDriftInsideOneGoverningGeneration(t *testing.T) {
	fixture := newStoreFixture(t, []action.Budget{storeBudget(
		"limit-generation", action.BudgetLimits{CallCount: 2}, action.BudgetResetNever,
	)})
	_, live := fixture.reserve(t, callID("c"))
	version, err := fixture.store.Release(
		context.Background(), live.Reservation.Identity, live.Snapshot.StateVersion,
	)
	if err != nil {
		t.Fatal(err)
	}
	request := fixture.request
	request.CallID, request.StateVersion = callID("a"), version
	if _, err := fixture.store.Reserve(context.Background(), ReserveRequest{
		Plan: compileStorePlan(t, fixture.serverFingerprint, []action.Budget{storeBudget(
			"limit-generation", action.BudgetLimits{CallCount: 3}, action.BudgetResetNever,
		)}),
		Request: request, Context: fixture.context, Authority: fixture.authority, Server: fixture.server,
	}); err == nil {
		t.Fatal("budget limits drifted without a governing generation change")
	} else {
		requireStateCode(t, err, action.ReasonStateCorrupt)
	}
}

func TestReservedUsageRejectsAggregateOverflow(t *testing.T) {
	lineage := "lineage"
	state := State{Reservations: []Reservation{
		{Charges: []ReservationCharge{{LineageIdentity: lineage, Reserved: action.BudgetUsage{CallCount: ^uint64(0)}}}},
		{Charges: []ReservationCharge{{LineageIdentity: lineage, Reserved: action.BudgetUsage{CallCount: 1}}}},
	}}
	if _, err := reservedUsageByLineage(state); err == nil {
		t.Fatal("aggregate reservation counter overflow was accepted")
	} else {
		requireStateCode(t, err, action.ReasonStateCorrupt)
	}
}

func TestBudgetStoreSerializesIndependentStoresWithoutOversubscription(t *testing.T) {
	limits := action.BudgetLimits{CallCount: 1}
	fixture := newStoreFixture(t, []action.Budget{storeBudget("single", limits, action.BudgetResetNever)})
	secondLease, err := AcquireIdentityKey(context.Background(), fixture.home)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := secondLease.Close(); err != nil {
			t.Errorf("close second identity-key lease: %v", err)
		}
	})
	second, err := OpenStore(StoreOptions{
		Home: fixture.home, Repository: fixture.repository, KeyLease: secondLease,
		Clock: fixture.clock, OwnerID: "owner-secondary",
	})
	if err != nil {
		t.Fatal(err)
	}
	version, err := fixture.store.CurrentStateVersion(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	requests := make([]ReserveRequest, 2)
	for index, id := range []string{callID("n"), callID("o")} {
		request := fixture.request
		request.CallID, request.StateVersion = id, version
		requests[index] = ReserveRequest{
			Plan: fixture.plan, Request: request, Context: fixture.context, Authority: fixture.authority,
			Server: fixture.server,
		}
	}
	stores := []*Store{fixture.store, second}
	results := make([]ReserveResult, 2)
	errorsSeen := make([]error, 2)
	start := make(chan struct{})
	var wait sync.WaitGroup
	for index := range stores {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			<-start
			results[index], errorsSeen[index] = stores[index].Reserve(context.Background(), requests[index])
		}(index)
	}
	close(start)
	wait.Wait()
	succeeded := 0
	for index := range results {
		if errorsSeen[index] == nil && results[index].Reservation != nil {
			succeeded++
			continue
		}
		requireStateCode(t, errorsSeen[index], action.ReasonStateUnavailable)
	}
	if succeeded != 1 {
		t.Fatalf("parallel reservations = %#v errors=%v, want exactly one", results, errorsSeen)
	}
	status, err := fixture.store.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if status.LiveReservations != 1 || status.Budgets[0].Reserved.CallCount != 1 {
		t.Fatalf("parallel budget status = %#v", status)
	}
}

func TestBudgetStoreRecoversPublishedTransactionAfterInterruption(t *testing.T) {
	fixture := newStoreFixture(t, []action.Budget{storeBudget(
		"recovery", action.BudgetLimits{CallCount: 1}, action.BudgetResetNever,
	)})
	previous, err := fixture.store.initialState()
	if err != nil {
		t.Fatal(err)
	}
	next := cloneState(previous)
	fixture.store.applyClock(&next, fixture.clock.snapshot)
	next.Revision = 1
	next.Digest, err = fixture.store.stateDigest(next)
	if err != nil {
		t.Fatal(err)
	}
	transaction, err := fixture.store.newTransaction(previous, false, next)
	if err != nil {
		t.Fatal(err)
	}
	if err := writeBoundedJSON(fixture.store.transactionPath, transaction, MaxStateTransaction); err != nil {
		t.Fatal(err)
	}
	version, err := fixture.store.CurrentStateVersion(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if version != next.Digest {
		t.Fatalf("recovered version = %q, want %q", version, next.Digest)
	}
	if _, err := os.Lstat(fixture.store.transactionPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("completed recovery journal remains: %v", err)
	}
}

func TestBudgetStorePreflightsOversizedStateBeforePublishingJournal(t *testing.T) {
	fixture := newStoreFixture(t, nil)
	previous, err := fixture.store.initialState()
	if err != nil {
		t.Fatal(err)
	}
	next := cloneState(previous)
	fixture.store.applyClock(&next, fixture.clock.snapshot)
	next.TerminalCalls = make([]TerminalCall, MaxTerminalCallRecords)
	for index := range next.TerminalCalls {
		callID := orderedCapacityCallID(index)
		next.TerminalCalls[index] = TerminalCall{
			CallID: callID, ReservationIdentity: fixture.key.Identity(
				DomainBudget, []byte("terminal-capacity"), []byte(callID),
			),
			Outcome: OutcomeReleased, CompletedAtUnix: fixture.clock.snapshot.Time.Unix(),
		}
	}
	published := false
	fixture.store.publish = func(string, []byte) error {
		published = true
		return nil
	}
	if err := fixture.store.writeState(previous, false, &next); err == nil {
		t.Fatal("oversized action state was accepted")
	} else {
		requireStateCode(t, err, action.ReasonStateUnavailable)
	}
	if published {
		t.Fatal("deterministically oversized state published a recovery journal")
	}
}

func orderedCapacityCallID(value int) string {
	encoded := []byte(strings.Repeat("a", 26))
	for index := len(encoded) - 1; value > 0; index-- {
		encoded[index] = byte('a' + value%26)
		value /= 26
	}
	return "act_" + string(encoded)
}

func TestBudgetStoreRejectsCrossRepositoryStateAndClosedKeyLease(t *testing.T) {
	fixture := newStoreFixture(t, []action.Budget{storeBudget(
		"binding", action.BudgetLimits{CallCount: 2}, action.BudgetResetNever,
	)})
	fixture.reserve(t, callID("p"))
	otherRepository := t.TempDir()
	otherStore, err := OpenStore(StoreOptions{
		Home: fixture.home, Repository: otherRepository, KeyLease: fixture.lease,
		Clock: fixture.clock, OwnerID: "owner-other",
	})
	if err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(fixture.store.statePath)
	if err != nil {
		t.Fatal(err)
	}
	writePrivateTestFile(t, otherStore.statePath, body)
	if _, err := otherStore.CurrentStateVersion(context.Background()); err == nil {
		t.Fatal("state from a different repository identity was accepted")
	} else {
		requireStateCode(t, err, action.ReasonStateCorrupt)
	}
	if err := fixture.lease.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.store.CurrentStateVersion(context.Background()); err == nil {
		t.Fatal("store remained usable after its identity-key lease closed")
	} else {
		requireStateCode(t, err, action.ReasonIdentityUnavailable)
	}
}

func TestBudgetStoreStatusContainsNoSecretAdjacentRawValues(t *testing.T) {
	fixture := newStoreFixture(t, []action.Budget{storeBudget(
		"privacy", action.BudgetLimits{CallCount: 2}, action.BudgetResetOperatorRun,
	)})
	fixture.reserve(t, callID("q"))
	status, err := fixture.store.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(status)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"credential-secret", "Run:42", "session_7", `"amount"`, `"target"`} {
		if strings.Contains(string(body), forbidden) {
			t.Fatalf("status exposed %q: %s", forbidden, body)
		}
	}
	if !strings.Contains(string(body), `"warehouse"`) || !strings.Contains(string(body), `"operator_bound"`) {
		t.Fatalf("status omitted safe labels or provenance: %s", body)
	}
}

func TestBudgetStoreStatusShowsEveryLiveReservationAndExactRemediation(t *testing.T) {
	fixture := newStoreFixture(t, []action.Budget{storeBudget(
		"status", action.BudgetLimits{CallCount: 4, Concurrent: 4}, action.BudgetResetNever,
	)})
	_, reserved := fixture.reserve(t, callID("a"))
	_, dispatched := fixture.reserve(t, callID("b"))
	_, err := fixture.store.MarkDispatched(
		context.Background(), dispatched.Reservation.Identity, dispatched.Snapshot.StateVersion,
	)
	if err != nil {
		t.Fatal(err)
	}
	_, indeterminate := fixture.reserve(t, callID("c"))
	version, err := fixture.store.MarkIndeterminate(
		context.Background(), indeterminate.Reservation.Identity, indeterminate.Snapshot.StateVersion,
	)
	if err != nil {
		t.Fatal(err)
	}
	status, err := fixture.store.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if status.StateVersion != version || len(status.Reservations) != 3 || status.LiveReservations != 3 ||
		status.Indeterminate != 1 || status.Capacity.Reservations != 3 ||
		status.Capacity.ReservationsMaximum != MaxReservations || status.Capacity.StateBytes <= 0 ||
		status.Capacity.StateBytes > status.Capacity.StateBytesMaximum {
		t.Fatalf("reservation status = %#v", status)
	}
	want := map[ReservationStatus]StateRemediation{
		ReservationReserved:      RemediationReleaseOrDispatch,
		ReservationDispatched:    RemediationSettleOrMarkUnknown,
		ReservationIndeterminate: RemediationReconcileIndeterminate,
	}
	seen := make(map[ReservationStatus]bool)
	for _, reservation := range status.Reservations {
		if reservation.Remediation != want[reservation.Status] || len(reservation.BudgetIDs) != 1 ||
			reservation.BudgetIDs[0] != "status" || reservation.Identity == "" || reservation.CallID == "" {
			t.Fatalf("reservation view = %#v", reservation)
		}
		seen[reservation.Status] = true
	}
	if len(seen) != len(want) || len(status.Remediations) != len(want) || reserved.Reservation == nil {
		t.Fatalf("status remediations = %#v, seen=%#v", status.Remediations, seen)
	}
}

func TestBudgetStoreLockTimeoutFailsClosed(t *testing.T) {
	fixture := newStoreFixture(t, []action.Budget{storeBudget(
		"lock", action.BudgetLimits{CallCount: 1}, action.BudgetResetNever,
	)})
	fixture.store.lockTimeout = 25 * time.Millisecond
	held, err := acquireFileLock(context.Background(), fixture.store.lockPath, StateLockTimeout)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := held.close(); err != nil {
			t.Errorf("close held state lock: %v", err)
		}
	}()
	if _, err := fixture.store.CurrentStateVersion(context.Background()); err == nil {
		t.Fatal("contended state lock did not fail closed")
	} else {
		requireStateCode(t, err, action.ReasonStateUnavailable)
	}
}

func TestBudgetStateStrictDecoderRejectsUnknownAndOversizedInput(t *testing.T) {
	tests := []struct {
		name  string
		write func(testing.TB, *Store)
	}{
		{
			name: "unknown field",
			write: func(t testing.TB, store *Store) {
				t.Helper()
				writePrivateTestFile(t, store.statePath, []byte(`{"unknown":true}`))
			},
		},
		{
			name: "oversized file",
			write: func(t testing.TB, store *Store) {
				t.Helper()
				writePrivateTestFile(t, store.statePath, nil)
				file, err := os.OpenFile(store.statePath, os.O_WRONLY|os.O_TRUNC, 0)
				if err != nil {
					t.Fatal(err)
				}
				if err := file.Truncate(MaxStateBytes + 1); err != nil {
					closeErr := file.Close()
					t.Fatalf("truncate oversized state: %v; close: %v", err, closeErr)
				}
				if err := file.Close(); err != nil {
					t.Fatal(err)
				}
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newStoreFixture(t, nil)
			test.write(t, fixture.store)
			if _, err := fixture.store.CurrentStateVersion(context.Background()); err == nil {
				t.Fatal("malformed state was accepted")
			} else {
				requireStateCode(t, err, action.ReasonStateCorrupt)
			}
		})
	}
}

func TestPolicyAuthorityVerifiesFreshObservedDigest(t *testing.T) {
	tests := []struct {
		name      string
		authority PolicyAuthority
		observed  string
		valid     bool
	}{
		{
			name: "pinned exact", authority: PolicyAuthority{
				Mode: action.AuthorityOperatorPinned, ExpectedLockDigest: storeLockDigest,
			}, observed: storeLockDigest, valid: true,
		},
		{
			name: "pinned mismatch", authority: PolicyAuthority{
				Mode: action.AuthorityOperatorPinned, ExpectedLockDigest: storeLockDigest,
			}, observed: storePolicyDigest,
		},
		{
			name: "repository managed", authority: PolicyAuthority{
				Mode: action.AuthorityRepositoryManaged,
			}, observed: storePolicyDigest, valid: true,
		},
		{
			name: "invalid observed", authority: PolicyAuthority{
				Mode: action.AuthorityRepositoryManaged,
			}, observed: "SHA256",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := test.authority.VerifyLockDigest(test.observed)
			if (err == nil) != test.valid {
				t.Fatalf("VerifyLockDigest error = %v, valid=%t", err, test.valid)
			}
		})
	}
}

func TestStorePathsRemainOutsideRepository(t *testing.T) {
	repository := t.TempDir()
	home := filepath.Join(repository, "operator-state")
	if err := ensurePrivateDirectory(home); err != nil {
		t.Fatal(err)
	}
	if _, err := createIdentityKey(home, time.Now().UTC(), strings.NewReader(strings.Repeat("z", 32))); err != nil {
		t.Fatal(err)
	}
	lease, err := AcquireIdentityKey(context.Background(), home)
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Close()
	if _, err := OpenStore(StoreOptions{Home: home, Repository: repository, KeyLease: lease}); err == nil {
		t.Fatal("repository-controlled state root was accepted")
	}
}

func TestOpenStoreActionBoundaryIsNeverRemovedByGenericProjectRetention(t *testing.T) {
	fixture := newStoreFixture(t, nil)
	otherRepository := t.TempDir()
	policy := retention.DefaultPolicy()
	policy.ProjectRoots = retention.ClassPolicy{MaxFiles: 1, MaxBytes: 1, MaxAge: time.Nanosecond}
	report := retention.Run(retention.Options{
		RepoRoot: otherRepository, StateRoot: fixture.home, Policy: policy,
		Now: fixture.clock.snapshot.Time.Add(365 * 24 * time.Hour), TempRoot: t.TempDir(),
	})
	if _, err := os.Stat(fixture.store.directory); err != nil {
		t.Fatalf("generic retention removed the action-state ownership boundary: %v", err)
	}
	if _, err := fixture.store.CurrentStateVersion(context.Background()); err != nil {
		t.Fatalf("action state became unavailable after generic retention: %v", err)
	}
	if !strings.Contains(strings.Join(report.Errors, "\n"), "protected project state uses") {
		t.Fatalf("over-budget protected action state was not reported: %+v", report)
	}
}
