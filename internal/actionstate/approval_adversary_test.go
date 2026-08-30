package actionstate

import (
	"bytes"
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"reconc.dev/reconc/internal/action"
	"reconc.dev/reconc/internal/actionapproval"
)

func TestApprovalReceiptIsConsumedOnceAcrossIndependentStores(t *testing.T) {
	fixture := newStoreFixture(t, []action.Budget{storeBudget(
		"parallel-approval", action.BudgetLimits{CallCount: 2, ApprovalCount: 2}, action.BudgetResetNever,
	)})
	input, reserved := fixture.reserve(t, callID("p"))
	issued := issueFixtureApproval(t, fixture, input, reserved)
	receipt := signedApprovalReceipt(t, issued, actionapproval.DecisionApprove)

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
		Clock: fixture.clock, OwnerID: "owner-primary",
	})
	if err != nil {
		t.Fatal(err)
	}
	stores := []*Store{fixture.store, second}
	const attempts = 8
	errorsSeen := make([]error, attempts)
	start := make(chan struct{})
	var wait sync.WaitGroup
	for index := range errorsSeen {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			<-start
			binding := issued.binding
			binding.Evaluation.Request.StateVersion = issued.issue.StateVersion
			_, errorsSeen[index] = stores[index%len(stores)].ConsumeApproval(
				context.Background(),
				ApprovalConsumeRequest{
					Binding: binding, Registry: issued.registry, Receipt: receipt,
					RequestState: issued.issue.RequestState, ExpectedStateVersion: issued.issue.StateVersion,
				},
			)
		}(index)
	}
	close(start)
	wait.Wait()
	succeeded := 0
	for _, consumeErr := range errorsSeen {
		if consumeErr == nil {
			succeeded++
			continue
		}
		requireStateCode(t, consumeErr, action.ReasonApprovalReplayed)
	}
	if succeeded != 1 {
		t.Fatalf("parallel approval successes = %d, want exactly one; errors=%v", succeeded, errorsSeen)
	}
	status, err := fixture.store.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(status.Budgets) != 1 || status.Budgets[0].Consumed.ApprovalCount != 1 ||
		len(status.ApprovalRecords) != 1 || status.ApprovalRecords[0].Status != actionapproval.StatusApproved {
		t.Fatalf("parallel approval state = %#v", status)
	}
}

func TestIndependentPendingApprovalsSurviveUnrelatedStateTransitions(t *testing.T) {
	fixture := newStoreFixture(t, []action.Budget{storeBudget(
		"pending-approvals", action.BudgetLimits{CallCount: 4, ApprovalCount: 4}, action.BudgetResetNever,
	)})
	firstInput, firstReserved := fixture.reserve(t, callID("x"))
	first := issueFixtureApproval(t, fixture, firstInput, firstReserved)
	secondInput, secondReserved := fixture.reserve(t, callID("y"))
	second := issueFixtureApproval(t, fixture, secondInput, secondReserved)
	if first.issue.StateVersion == second.issue.StateVersion {
		t.Fatal("independent approval issuances did not advance global state")
	}
	if _, err := consumeFixtureApproval(t, fixture, first, actionapproval.DecisionApprove); err != nil {
		t.Fatalf("first approval was invalidated by the second issuance: %v", err)
	}
	if _, err := consumeFixtureApproval(t, fixture, second, actionapproval.DecisionApprove); err != nil {
		t.Fatalf("second approval was invalidated by the first consumption: %v", err)
	}
	status, err := fixture.store.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if status.PendingApprovals != 0 || len(status.ApprovalRecords) != 2 ||
		status.Budgets[0].Consumed.ApprovalCount != 2 {
		t.Fatalf("independent pending approvals = %#v", status)
	}
}

func TestOwnerAbandonmentTerminalizesOnlyOwnedPendingApprovals(t *testing.T) {
	fixture := newStoreFixture(t, []action.Budget{storeBudget(
		"owner-abandonment",
		action.BudgetLimits{CallCount: 8, ApprovalCount: 8, ResultBytes: 800, Concurrent: 4},
		action.BudgetResetNever,
	)})
	preInput, preReserved := fixture.reserve(t, callID("g"))
	pre := issueFixtureApproval(t, fixture, preInput, preReserved)

	postInput, postReserved := fixture.reserve(t, callID("h"))
	postPre := issueFixtureApproval(t, fixture, postInput, postReserved)
	postPreResult, err := consumeFixtureApproval(t, fixture, postPre, actionapproval.DecisionApprove)
	if err != nil {
		t.Fatal(err)
	}
	version, err := fixture.store.MarkDispatched(
		context.Background(), postReserved.Reservation.Identity, postPreResult.StateVersion,
	)
	if err != nil {
		t.Fatal(err)
	}
	result, err := action.ParseJSON([]byte(`{"target":"production","ok":true}`))
	if err != nil {
		t.Fatal(err)
	}
	postRequest := postInput.Request
	postRequest.Phase = action.PhasePostResult
	postRequest.Arguments = nil
	postRequest.Result = &result
	postRequest.StateVersion = version
	postBinding := ApprovalBinding{
		Plan: fixture.plan, BudgetRequest: postInput.Request,
		Context: fixture.context, Authority: fixture.authority, Server: fixture.server,
		Evaluation: action.EvaluationInput{
			Request: postRequest, SourceIdentity: strings.Repeat("8", 64),
			ContextIdentity: fixture.context.ContextIdentity, ExecutableDigest: fixture.server.ExecutableDigest,
			Principal: fixture.context.Principal, CredentialLabels: credentialLabels(fixture.context.Credentials),
			Budget:    absentBudgetSnapshot(version),
			Approval:  action.ApprovalSnapshot{Status: action.ApprovalNone, Identity: "approval-none"},
			Taint:     action.TaintSnapshot{Status: action.TaintClean, Identity: "taint-clean"},
			Lifecycle: action.LifecycleActive, CachePolicyVersion: action.CacheIdentityVersion,
		},
	}
	postIssue, err := fixture.store.IssueApproval(context.Background(), ApprovalIssueRequest{
		Binding: postBinding, AuthorityPolicyID: "production-writes", TTL: 30 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	post := issuedApprovalFixture{
		binding: postBinding, issue: postIssue, registry: postPre.registry, privateKey: postPre.privateKey,
	}

	_, indeterminate := fixture.reserve(t, callID("i"))
	if _, err := fixture.store.MarkIndeterminate(
		context.Background(), indeterminate.Reservation.Identity, indeterminate.Snapshot.StateVersion,
	); err != nil {
		t.Fatal(err)
	}

	secondaryLease, err := AcquireIdentityKey(context.Background(), fixture.home)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := secondaryLease.Close(); err != nil {
			t.Errorf("close secondary identity-key lease: %v", err)
		}
	})
	secondaryStore, err := OpenStore(StoreOptions{
		Home: fixture.home, Repository: fixture.repository, KeyLease: secondaryLease,
		Clock: fixture.clock, OwnerID: "owner-secondary",
	})
	if err != nil {
		t.Fatal(err)
	}
	secondaryFixture := *fixture
	secondaryFixture.store = secondaryStore
	secondaryInput, secondaryReserved := secondaryFixture.reserve(t, callID("j"))
	secondary := issueFixtureApproval(t, &secondaryFixture, secondaryInput, secondaryReserved)

	current, err := fixture.store.CurrentStateVersion(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	staleAuthorization, err := NewOwnerAbandonmentAuthorization(fixture.key, "owner-primary", pre.issue.StateVersion)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.store.MarkOwnerAbandoned(
		context.Background(), "owner-primary", pre.issue.StateVersion, staleAuthorization,
	); err == nil {
		t.Fatal("stale owner abandonment version was accepted")
	} else {
		requireStateCode(t, err, action.ReasonStateUnavailable)
	}
	authorization, err := NewOwnerAbandonmentAuthorization(fixture.key, "owner-primary", current)
	if err != nil {
		t.Fatal(err)
	}
	version, err = fixture.store.MarkOwnerAbandoned(
		context.Background(), "owner-primary", current, authorization,
	)
	if err != nil {
		t.Fatal(err)
	}
	status, err := fixture.store.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	approvalStatuses := make(map[string]actionapproval.Status, len(status.ApprovalRecords))
	for _, record := range status.ApprovalRecords {
		approvalStatuses[record.RequestID] = record.Status
	}
	if approvalStatuses[pre.issue.Request.RequestID] != actionapproval.StatusUnavailable ||
		approvalStatuses[post.issue.Request.RequestID] != actionapproval.StatusUnavailable ||
		approvalStatuses[postPre.issue.Request.RequestID] != actionapproval.StatusApproved ||
		approvalStatuses[secondary.issue.Request.RequestID] != actionapproval.StatusPending {
		t.Fatalf("owner abandonment approval statuses = %#v", approvalStatuses)
	}
	reservationStatuses := make(map[string]ReservationStatus, len(status.Reservations))
	for _, reservation := range status.Reservations {
		reservationStatuses[reservation.CallID] = reservation.Status
	}
	if reservationStatuses[preReserved.Reservation.CallID] != ReservationIndeterminate ||
		reservationStatuses[postReserved.Reservation.CallID] != ReservationIndeterminate ||
		reservationStatuses[indeterminate.Reservation.CallID] != ReservationIndeterminate ||
		reservationStatuses[secondaryReserved.Reservation.CallID] != ReservationReserved ||
		status.PendingApprovals != 1 || status.Indeterminate != 3 ||
		status.Budgets[0].Consumed.ApprovalCount != 1 || status.Budgets[0].Reserved.ApprovalCount != 1 {
		t.Fatalf("owner abandonment state = %#v", status)
	}
	for _, test := range []struct {
		name   string
		issued issuedApprovalFixture
	}{
		{name: "pre-call", issued: pre},
		{name: "post-result", issued: post},
	} {
		t.Run(test.name+" replay", func(t *testing.T) {
			result, err := consumeFixtureApproval(t, fixture, test.issued, actionapproval.DecisionApprove)
			requireStateCode(t, err, action.ReasonApprovalReplayed)
			if result.Status != actionapproval.StatusUnavailable || result.StateVersion != version {
				t.Fatalf("abandoned approval replay = %#v", result)
			}
		})
	}
	retryAuthorization, err := NewOwnerAbandonmentAuthorization(fixture.key, "owner-primary", version)
	if err != nil {
		t.Fatal(err)
	}
	if retry, err := fixture.store.MarkOwnerAbandoned(
		context.Background(), "owner-primary", version, retryAuthorization,
	); err != nil || retry != version {
		t.Fatalf("idempotent owner abandonment = %q, %v", retry, err)
	}
	if _, err := consumeFixtureApproval(t, &secondaryFixture, secondary, actionapproval.DecisionApprove); err != nil {
		t.Fatalf("unrelated owner approval was changed: %v", err)
	}
}

func TestOwnerAbandonmentRecoversAtomicApprovalTransitionAfterPublicationFailure(t *testing.T) {
	fixture := newStoreFixture(t, []action.Budget{storeBudget(
		"owner-abandonment-recovery",
		action.BudgetLimits{CallCount: 2, ApprovalCount: 2, Concurrent: 2},
		action.BudgetResetNever,
	)})
	input, reserved := fixture.reserve(t, callID("k"))
	issued := issueFixtureApproval(t, fixture, input, reserved)
	authorization, err := NewOwnerAbandonmentAuthorization(
		fixture.key, "owner-primary", issued.issue.StateVersion,
	)
	if err != nil {
		t.Fatal(err)
	}
	publish := fixture.store.publish
	fixture.store.publish = func(path string, body []byte) error {
		if path == fixture.store.statePath {
			return errors.New("simulated state publication failure")
		}
		return publish(path, body)
	}
	if _, err := fixture.store.MarkOwnerAbandoned(
		context.Background(), "owner-primary", issued.issue.StateVersion, authorization,
	); err == nil {
		t.Fatal("owner abandonment survived simulated state publication failure")
	}
	if _, err := os.Lstat(fixture.store.transactionPath); err != nil {
		t.Fatalf("owner abandonment recovery journal missing: %v", err)
	}
	fixture.store.publish = publish
	status, err := fixture.store.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if status.PendingApprovals != 0 || status.Indeterminate != 1 ||
		len(status.ApprovalRecords) != 1 || status.ApprovalRecords[0].Status != actionapproval.StatusUnavailable ||
		status.Budgets[0].Reserved.ApprovalCount != 0 {
		t.Fatalf("recovered owner abandonment state = %#v", status)
	}
	if _, err := os.Lstat(fixture.store.transactionPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("completed owner abandonment recovery journal remains: %v", err)
	}
	result, err := consumeFixtureApproval(t, fixture, issued, actionapproval.DecisionApprove)
	requireStateCode(t, err, action.ReasonApprovalReplayed)
	if result.Status != actionapproval.StatusUnavailable {
		t.Fatalf("recovered abandoned approval replay = %#v", result)
	}
}

func TestDuplicateReceiptIDTerminalizesTheSecondPendingApproval(t *testing.T) {
	fixture := newStoreFixture(t, []action.Budget{storeBudget(
		"duplicate-receipt", action.BudgetLimits{CallCount: 4, ApprovalCount: 4}, action.BudgetResetNever,
	)})
	firstInput, firstReserved := fixture.reserve(t, callID("a"))
	first := issueFixtureApproval(t, fixture, firstInput, firstReserved)
	secondInput, secondReserved := fixture.reserve(t, callID("b"))
	second := issueFixtureApproval(t, fixture, secondInput, secondReserved)

	firstReceipt := signedApprovalReceipt(t, first, actionapproval.DecisionApprove)
	secondReceipt := signedApprovalReceipt(t, second, actionapproval.DecisionApprove)
	if _, err := consumeSignedApproval(t, fixture, first, firstReceipt); err != nil {
		t.Fatal(err)
	}
	result, err := consumeSignedApproval(t, fixture, second, secondReceipt)
	requireStateCode(t, err, action.ReasonApprovalReplayed)
	if result.Status != actionapproval.StatusReplayed {
		t.Fatalf("duplicate receipt result = %#v", result)
	}
	status, err := fixture.store.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if status.PendingApprovals != 0 || status.LiveReservations != 1 ||
		status.TerminalCallCount != 1 || status.Budgets[0].Consumed.ApprovalCount != 1 {
		t.Fatalf("duplicate receipt state = %#v", status)
	}
}

func TestApprovalFailureRecordsDenialAndTerminalizesAtCapacity(t *testing.T) {
	fixture := newStoreFixture(t, []action.Budget{storeBudget(
		"approval-denial", action.BudgetLimits{CallCount: 4, DeniedCount: 1, ApprovalCount: 4}, action.BudgetResetNever,
	)})
	for index, suffix := range []string{"c", "d"} {
		input, reserved := fixture.reserve(t, callID(suffix))
		issued := issueFixtureApproval(t, fixture, input, reserved)
		result, err := consumeFixtureApproval(t, fixture, issued, actionapproval.DecisionReject)
		requireStateCode(t, err, action.ReasonApprovalRejected)
		if result.Status != actionapproval.StatusRejected {
			t.Fatalf("rejected approval %d = %#v", index, result)
		}
		if index == 1 && !strings.Contains(err.Error(), string(action.ReasonBudgetExhausted)) {
			t.Fatalf("second rejection did not report denial capacity exhaustion: %v", err)
		}
	}
	status, err := fixture.store.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if status.PendingApprovals != 0 || status.LiveReservations != 0 || status.TerminalCallCount != 2 ||
		status.Budgets[0].Consumed.DeniedCount != 1 || status.Budgets[0].Consumed.ApprovalCount != 0 {
		t.Fatalf("approval denial state = %#v", status)
	}
}

func TestApprovalPersistenceFailureDoesNotConsumeReceiptOrBudget(t *testing.T) {
	fixture := newStoreFixture(t, []action.Budget{storeBudget(
		"approval-disk", action.BudgetLimits{CallCount: 2, ApprovalCount: 2}, action.BudgetResetNever,
	)})
	input, reserved := fixture.reserve(t, callID("q"))
	issued := issueFixtureApproval(t, fixture, input, reserved)
	receipt := signedApprovalReceipt(t, issued, actionapproval.DecisionApprove)
	binding := issued.binding
	binding.Evaluation.Request.StateVersion = issued.issue.StateVersion
	publish := fixture.store.publish
	fixture.store.publish = func(string, []byte) error { return errors.New("simulated disk full") }
	_, err := fixture.store.ConsumeApproval(context.Background(), ApprovalConsumeRequest{
		Binding: binding, Registry: issued.registry, Receipt: receipt,
		RequestState: issued.issue.RequestState, ExpectedStateVersion: issued.issue.StateVersion,
	})
	if err == nil {
		t.Fatal("approval consume succeeded despite persistence failure")
	}
	fixture.store.publish = publish

	status, err := fixture.store.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if status.PendingApprovals != 1 || status.Budgets[0].Consumed.ApprovalCount != 0 {
		t.Fatalf("failed approval publication changed durable state: %#v", status)
	}
	if _, err := fixture.store.ConsumeApproval(context.Background(), ApprovalConsumeRequest{
		Binding: binding, Registry: issued.registry, Receipt: receipt,
		RequestState: issued.issue.RequestState, ExpectedStateVersion: issued.issue.StateVersion,
	}); err != nil {
		t.Fatalf("receipt was consumed by failed publication: %v", err)
	}
}

func TestApprovalExpiryReconciliationPersistenceFailureIsAtomic(t *testing.T) {
	fixture := newStoreFixture(t, []action.Budget{storeBudget(
		"approval-reconcile-disk", action.BudgetLimits{CallCount: 2, ApprovalCount: 2}, action.BudgetResetNever,
	)})
	input, reserved := fixture.reserve(t, callID("d"))
	issueFixtureApproval(t, fixture, input, reserved)
	fixture.clock.set(fixture.clock.snapshot.Time.Add(time.Minute), "test-clock")

	publish := fixture.store.publish
	fixture.store.publish = func(string, []byte) error { return errors.New("simulated disk full") }
	if _, err := fixture.store.ReconcileExpiredApprovals(context.Background()); err == nil {
		t.Fatal("approval expiry reconciliation succeeded despite persistence failure")
	}
	fixture.store.publish = publish

	status, err := fixture.store.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if status.PendingApprovals != 1 || status.LiveReservations != 1 || status.TerminalCallCount != 0 {
		t.Fatalf("failed reconciliation changed durable state: %#v", status)
	}
	reconciled, err := fixture.store.ReconcileExpiredApprovals(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(reconciled.Expired) != 1 || reconciled.Expired[0].Status != actionapproval.StatusExpired {
		t.Fatalf("reconciliation after restored persistence = %#v", reconciled)
	}
}

func TestApprovalExpiryReconciliationIsSingleWriterAcrossIndependentStores(t *testing.T) {
	fixture := newStoreFixture(t, []action.Budget{storeBudget(
		"approval-reconcile-race", action.BudgetLimits{CallCount: 2, ApprovalCount: 2}, action.BudgetResetNever,
	)})
	input, reserved := fixture.reserve(t, callID("e"))
	issueFixtureApproval(t, fixture, input, reserved)
	fixture.clock.set(fixture.clock.snapshot.Time.Add(time.Minute), "test-clock")

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
		Clock: fixture.clock, OwnerID: "owner-primary",
	})
	if err != nil {
		t.Fatal(err)
	}

	stores := []*Store{fixture.store, second}
	results := make([]ApprovalReconcileResult, len(stores))
	errorsSeen := make([]error, len(stores))
	start := make(chan struct{})
	var wait sync.WaitGroup
	for index := range stores {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			<-start
			results[index], errorsSeen[index] = stores[index].ReconcileExpiredApprovals(context.Background())
		}(index)
	}
	close(start)
	wait.Wait()

	expired := 0
	for index, reconcileErr := range errorsSeen {
		if reconcileErr != nil {
			t.Fatalf("concurrent reconciliation %d: %v", index, reconcileErr)
		}
		expired += len(results[index].Expired)
	}
	if expired != 1 {
		t.Fatalf("concurrent reconciliation expired %d records, want exactly one; results=%#v", expired, results)
	}
	status, err := fixture.store.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if status.PendingApprovals != 0 || status.LiveReservations != 0 || status.TerminalCallCount != 1 {
		t.Fatalf("concurrent reconciliation state = %#v", status)
	}
}

func TestApprovalMalformedReceiptAndCancellationTerminalizeExactlyOnce(t *testing.T) {
	tests := []struct {
		name     string
		call     string
		finalize *actionapproval.Status
		want     actionapproval.Status
		code     action.ReasonCode
	}{
		{name: "malformed", call: "r", want: actionapproval.StatusMalformed, code: action.ReasonApprovalInvalid},
		{name: "cancelled", call: "s", finalize: approvalStatus(actionapproval.StatusCancelled), want: actionapproval.StatusCancelled},
		{name: "unavailable", call: "t", finalize: approvalStatus(actionapproval.StatusUnavailable), want: actionapproval.StatusUnavailable},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newStoreFixture(t, []action.Budget{storeBudget(
				"terminal-approval", action.BudgetLimits{CallCount: 2, ApprovalCount: 2}, action.BudgetResetNever,
			)})
			input, reserved := fixture.reserve(t, callID(test.call))
			issued := issueFixtureApproval(t, fixture, input, reserved)
			var result ApprovalConsumeResult
			var err error
			if test.finalize == nil {
				binding := issued.binding
				binding.Evaluation.Request.StateVersion = issued.issue.StateVersion
				result, err = fixture.store.ConsumeApproval(context.Background(), ApprovalConsumeRequest{
					Binding: binding, Registry: issued.registry, Receipt: []byte(`{}`),
					RequestState: issued.issue.RequestState, ExpectedStateVersion: issued.issue.StateVersion,
				})
				requireStateCode(t, err, test.code)
			} else {
				result, err = fixture.store.FinalizeApproval(context.Background(), ApprovalFinalizeRequest{
					RequestState: issued.issue.RequestState, ExpectedStateVersion: issued.issue.StateVersion,
					Status: *test.finalize,
				})
				if err != nil {
					t.Fatal(err)
				}
			}
			if result.Status != test.want {
				t.Fatalf("terminal approval = %#v", result)
			}
			status, err := fixture.store.Status(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if status.PendingApprovals != 0 || status.LiveReservations != 0 || status.TerminalCallCount != 1 {
				t.Fatalf("terminal approval state = %#v", status)
			}
		})
	}
}

func TestApprovalConsumeRejectsRegistryWithoutTrustedLoaderProvenance(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*LoadedApprovalRegistry)
	}{
		{name: "zero registry", mutate: func(value *LoadedApprovalRegistry) { *value = LoadedApprovalRegistry{} }},
		{name: "identity mismatch", mutate: func(value *LoadedApprovalRegistry) { value.identity = "sha256:" + strings.Repeat("0", 64) }},
		{name: "missing path provenance", mutate: func(value *LoadedApprovalRegistry) { value.path = "" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newStoreFixture(t, []action.Budget{storeBudget(
				"trusted-registry", action.BudgetLimits{CallCount: 2, ApprovalCount: 2}, action.BudgetResetNever,
			)})
			input, reserved := fixture.reserve(t, callID("m"))
			issued := issueFixtureApproval(t, fixture, input, reserved)
			registry := issued.registry
			test.mutate(&registry)
			binding := issued.binding
			binding.Evaluation.Request.StateVersion = issued.issue.StateVersion
			result, err := fixture.store.ConsumeApproval(context.Background(), ApprovalConsumeRequest{
				Binding: binding, Registry: registry,
				Receipt:      signedApprovalReceipt(t, issued, actionapproval.DecisionApprove),
				RequestState: issued.issue.RequestState, ExpectedStateVersion: issued.issue.StateVersion,
			})
			requireStateCode(t, err, action.ReasonAuthorityUnavailable)
			if result.Status != actionapproval.StatusUnavailable {
				t.Fatalf("untrusted registry result = %#v", result)
			}
		})
	}
}

func TestApprovalRequestStateTamperAndPendingReservationCorruptionFailClosed(t *testing.T) {
	fixture := newStoreFixture(t, []action.Budget{storeBudget(
		"approval-integrity", action.BudgetLimits{CallCount: 2, ApprovalCount: 2}, action.BudgetResetNever,
	)})
	input, reserved := fixture.reserve(t, callID("u"))
	issued := issueFixtureApproval(t, fixture, input, reserved)
	replacement := "A"
	if issued.issue.RequestState[len(issued.issue.RequestState)-1:] == replacement {
		replacement = "B"
	}
	tampered := issued.issue.RequestState[:len(issued.issue.RequestState)-1] + replacement
	binding := issued.binding
	binding.Evaluation.Request.StateVersion = issued.issue.StateVersion
	if _, err := fixture.store.ConsumeApproval(context.Background(), ApprovalConsumeRequest{
		Binding: binding, Registry: issued.registry, Receipt: signedApprovalReceipt(t, issued, actionapproval.DecisionApprove),
		RequestState: tampered, ExpectedStateVersion: issued.issue.StateVersion,
	}); err == nil {
		t.Fatal("tampered approval request state was accepted")
	}

	var state State
	if err := fixture.store.withLock(context.Background(), func() error {
		var err error
		state, _, err = fixture.store.loadState()
		return err
	}); err != nil {
		t.Fatal(err)
	}
	mutated := cloneState(state)
	mutated.Reservations = []Reservation{}
	digest, err := fixture.store.stateDigest(mutated)
	if err != nil {
		t.Fatal(err)
	}
	mutated.Digest = digest
	if err := fixture.store.validateState(mutated, true); err == nil {
		t.Fatal("pending approval without its reservation was accepted as valid state")
	} else {
		requireStateCode(t, err, action.ReasonStateCorrupt)
	}
}

func TestApprovalConsumptionRejectsEveryTrustedBindingDrift(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(testing.TB, *storeFixture, *ApprovalBinding)
	}{
		{name: "evaluation context", mutate: func(_ testing.TB, _ *storeFixture, value *ApprovalBinding) {
			value.Evaluation.ContextIdentity = "context-drift"
		}},
		{name: "evaluation executable", mutate: func(_ testing.TB, _ *storeFixture, value *ApprovalBinding) {
			value.Evaluation.ExecutableDigest = storeNextExecutable
		}},
		{name: "evaluation principal", mutate: func(_ testing.TB, _ *storeFixture, value *ApprovalBinding) {
			value.Evaluation.Principal = "other-principal"
		}},
		{name: "evaluation credentials", mutate: func(_ testing.TB, _ *storeFixture, value *ApprovalBinding) {
			value.Evaluation.CredentialLabels = []string{"other"}
		}},
		{name: "evaluation approval", mutate: func(_ testing.TB, _ *storeFixture, value *ApprovalBinding) {
			value.Evaluation.Approval = action.ApprovalSnapshot{Status: action.ApprovalCurrentUnconsumed, Identity: "approval-other"}
		}},
		{name: "evaluation lifecycle", mutate: func(_ testing.TB, _ *storeFixture, value *ApprovalBinding) {
			value.Evaluation.Lifecycle = action.LifecycleShutdown
		}},
		{name: "source", mutate: func(_ testing.TB, _ *storeFixture, value *ApprovalBinding) {
			value.Evaluation.SourceIdentity = storeNextPolicyDigest
		}},
		{name: "taint", mutate: func(_ testing.TB, _ *storeFixture, value *ApprovalBinding) {
			value.Evaluation.Taint.Identity = "taint-other"
		}},
		{name: "repository effect", mutate: func(_ testing.TB, _ *storeFixture, value *ApprovalBinding) {
			value.Evaluation.RepositoryEffect = &action.RepositoryEffectCandidate{
				Decision: action.DecisionBlock, Reason: action.ReasonRuleMatched,
				RuleIDs: []string{"effect-rule"}, Identity: "effect-other",
			}
		}},
		{name: "operator context", mutate: func(_ testing.TB, _ *storeFixture, value *ApprovalBinding) {
			value.Context.Principal = "other-principal"
		}},
		{name: "downstream executable", mutate: func(_ testing.TB, _ *storeFixture, value *ApprovalBinding) {
			value.Server.ExecutableDigest = storeNextExecutable
		}},
		{name: "policy authority", mutate: func(_ testing.TB, _ *storeFixture, value *ApprovalBinding) {
			value.Authority.ExpectedLockDigest = storePolicyDigest
		}},
		{name: "request policy", mutate: func(_ testing.TB, _ *storeFixture, value *ApprovalBinding) {
			value.Evaluation.Request.PolicyDigest = storeNextPolicyDigest
		}},
		{name: "request argument", mutate: func(t testing.TB, _ *storeFixture, value *ApprovalBinding) {
			arguments, err := action.ParseJSON([]byte(`{"amount":2,"target":"staging"}`))
			if err != nil {
				t.Fatal(err)
			}
			value.Evaluation.Request.Arguments = &arguments
		}},
		{name: "compiled plan", mutate: func(t testing.TB, fixture *storeFixture, value *ApprovalBinding) {
			value.Plan = compileStorePlan(t, fixture.serverFingerprint, []action.Budget{
				storeBudget("changed-plan", action.BudgetLimits{CallCount: 2}, action.BudgetResetNever),
			})
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newStoreFixture(t, []action.Budget{storeBudget(
				"binding-approval", action.BudgetLimits{CallCount: 2, ApprovalCount: 2}, action.BudgetResetNever,
			)})
			input, reserved := fixture.reserve(t, callID("v"))
			issued := issueFixtureApproval(t, fixture, input, reserved)
			binding := issued.binding
			binding.Evaluation.CredentialLabels = append([]string(nil), issued.binding.Evaluation.CredentialLabels...)
			binding.Context.Credentials = append([]CredentialBinding(nil), issued.binding.Context.Credentials...)
			binding.Evaluation.Request.StateVersion = issued.issue.StateVersion
			test.mutate(t, fixture, &binding)
			if _, err := fixture.store.ConsumeApproval(context.Background(), ApprovalConsumeRequest{
				Binding: binding, Registry: issued.registry,
				Receipt:      signedApprovalReceipt(t, issued, actionapproval.DecisionApprove),
				RequestState: issued.issue.RequestState, ExpectedStateVersion: issued.issue.StateVersion,
			}); err == nil {
				t.Fatal("drifted approval binding was accepted")
			}
			status, err := fixture.store.Status(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if status.PendingApprovals != 1 || status.LiveReservations != 1 {
				t.Fatalf("binding drift mutated pending state: %#v", status)
			}
		})
	}
}

func TestApprovalTrustedClockRollbackAndPrematureExpiryFailClosed(t *testing.T) {
	fixture := newStoreFixture(t, []action.Budget{storeBudget(
		"approval-clock", action.BudgetLimits{CallCount: 2, ApprovalCount: 2}, action.BudgetResetNever,
	)})
	input, reserved := fixture.reserve(t, callID("w"))
	issued := issueFixtureApproval(t, fixture, input, reserved)
	if _, err := fixture.store.FinalizeApproval(context.Background(), ApprovalFinalizeRequest{
		RequestState: issued.issue.RequestState, ExpectedStateVersion: issued.issue.StateVersion,
		Status: actionapproval.StatusExpired,
	}); err == nil {
		t.Fatal("approval was expired before its trusted expiry")
	}
	fixture.clock.set(fixture.clock.snapshot.Time.Add(-1), "test-clock")
	binding := issued.binding
	binding.Evaluation.Request.StateVersion = issued.issue.StateVersion
	if _, err := fixture.store.ConsumeApproval(context.Background(), ApprovalConsumeRequest{
		Binding: binding, Registry: issued.registry,
		Receipt:      signedApprovalReceipt(t, issued, actionapproval.DecisionApprove),
		RequestState: issued.issue.RequestState, ExpectedStateVersion: issued.issue.StateVersion,
	}); err == nil {
		t.Fatal("approval consumption accepted trusted-clock rollback")
	}
}

func signedApprovalReceipt(
	t testing.TB,
	issued issuedApprovalFixture,
	decision actionapproval.Decision,
) []byte {
	t.Helper()
	signedAt, err := time.Parse(time.RFC3339Nano, issued.issue.Request.IssuedAt)
	if err != nil {
		t.Fatal(err)
	}
	_, receipt, err := actionapproval.SignReceipt(
		issued.issue.Request, "security-primary", issued.privateKey, decision,
		signedAt,
		bytes.NewReader(bytes.Repeat([]byte{0x71}, 16)),
	)
	if err != nil {
		t.Fatal(err)
	}
	return receipt
}

func consumeSignedApproval(
	t testing.TB,
	fixture *storeFixture,
	issued issuedApprovalFixture,
	receipt []byte,
) (ApprovalConsumeResult, error) {
	t.Helper()
	binding := issued.binding
	binding.Evaluation.Request.StateVersion = issued.issue.StateVersion
	return fixture.store.ConsumeApproval(context.Background(), ApprovalConsumeRequest{
		Binding: binding, Registry: issued.registry, Receipt: receipt,
		RequestState: issued.issue.RequestState, ExpectedStateVersion: issued.issue.StateVersion,
	})
}

func approvalStatus(status actionapproval.Status) *actionapproval.Status {
	return &status
}
