package actionstate

import (
	"context"
	"encoding/binary"
	"math/rand/v2"
	"sort"
	"strings"
	"testing"
	"time"

	"reconc.dev/reconc/internal/action"
	"reconc.dev/reconc/internal/actionapproval"
)

func admissionFixture(t testing.TB) *storeFixture {
	t.Helper()
	return newStoreFixture(t, []action.Budget{storeBudget(
		"admission", action.BudgetLimits{CallCount: 1000, ApprovalCount: 1000}, action.BudgetResetNever,
	)})
}

func (f *storeFixture) tryReserve(t testing.TB, callID string) error {
	t.Helper()
	version, err := f.store.CurrentStateVersion(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	request := f.request
	request.CallID, request.StateVersion = callID, version
	_, err = f.store.Reserve(context.Background(), ReserveRequest{
		Plan: f.plan, Request: request, Context: f.context, Authority: f.authority, Server: f.server,
	})
	return err
}

func (f *storeFixture) reserveAndRelease(t testing.TB, callID string) {
	t.Helper()
	_, reserved := f.reserve(t, callID)
	if _, err := f.store.Release(context.Background(), reserved.Reservation.Identity, reserved.Snapshot.StateVersion); err != nil {
		t.Fatal(err)
	}
}

func (f *storeFixture) terminalCallIDs(t testing.TB) map[string]bool {
	t.Helper()
	state, _, err := f.store.loadState()
	if err != nil {
		t.Fatal(err)
	}
	ids := make(map[string]bool, len(state.TerminalCalls))
	for _, call := range state.TerminalCalls {
		ids[call.CallID] = true
	}
	return ids
}

// callIDIssuedAt returns a call identity this fixture's store would have
// issued at the given time.
func (f *storeFixture) callIDIssuedAt(t testing.TB, at time.Time, random uint32) string {
	t.Helper()
	var raw [callIdentityBytes]byte
	binary.BigEndian.PutUint64(raw[:8], uint64(at.Unix())<<24)
	binary.BigEndian.PutUint32(raw[callIdentitySeconds:], random)
	mac, ok := f.store.callIdentityMAC(raw[:callIdentitySeconds+callIdentityRandom])
	if !ok {
		t.Fatal("derive call identity MAC")
	}
	copy(raw[callIdentitySeconds+callIdentityRandom:], mac[:])
	return encodeCallIdentity(raw)
}

func (f *storeFixture) advance(duration time.Duration) {
	snapshot, _ := f.clock.Snapshot()
	f.clock.set(snapshot.Time.Add(duration), snapshot.Source)
}

// A store-issued call identity carries its issue second and a MAC bound to
// the store key and repository; any other identity fails the MAC.
func TestStoreIssuedCallIdentitiesBindIssueTimeKeyAndRepository(t *testing.T) {
	fixture := newStoreFixture(t, nil)
	id := fixture.issuedCallID(t, "a")
	issued, ok := callIdentityIssuedAt(id)
	if !validCallID(id) || !ok || issued.Unix() != fixture.clock.snapshot.Time.Unix() || !fixture.store.issuedCallIdentity(id) {
		t.Fatalf("issued call identity %q decodes to %v, %t", id, issued, ok)
	}
	if other := newStoreFixture(t, nil); other.store.issuedCallIdentity(id) {
		t.Fatal("another repository's store accepted the call identity")
	}
	if fixture.store.issuedCallIdentity(unissuedCallID("a")) {
		t.Fatal("an unissued call identity passed the MAC")
	}
	for index := len(callIdentityPrefix); index < len(id); index++ {
		changed := []byte(id)
		changed[index] = callIdentityAlphabet[(strings.IndexByte(callIdentityAlphabet, id[index])+1)%len(callIdentityAlphabet)]
		if fixture.store.issuedCallIdentity(string(changed)) {
			t.Fatalf("a call identity changed at character %d passed the MAC", index)
		}
	}
	random := rand.New(rand.NewPCG(19, 623))
	for range 1000 {
		var raw [callIdentityBytes]byte
		for index := range raw {
			raw[index] = byte(random.Uint32())
		}
		raw[callIdentityBytes-1] &= callIdentityLastMask
		if decoded, ok := decodeCallIdentity(encodeCallIdentity(raw)); !ok || decoded != raw {
			t.Fatalf("call identity bits %x did not round-trip", raw)
		}
	}
}

// A reservation admits only a store-issued call identity whose issue second
// lies within the admission window around the trusted clock.
func TestReservationAdmitsOnlyFreshStoreIssuedCallIdentities(t *testing.T) {
	fixture := admissionFixture(t)
	now := fixture.clock.snapshot.Time
	requireStateCode(t, fixture.tryReserve(t, unissuedCallID("u")), action.ReasonIdentityUnavailable)
	for index, test := range []struct {
		issued   time.Time
		admitted bool
	}{
		{now.Add(-CallIdentityAdmissionWindow - time.Second), false},
		{now.Add(-CallIdentityAdmissionWindow + time.Second), true},
		{now, true},
		{now.Add(CallIdentityClockSkew - time.Second), true},
		{now.Add(CallIdentityClockSkew + time.Second), false},
	} {
		err := fixture.tryReserve(t, fixture.callIDIssuedAt(t, test.issued, uint32(index)))
		if test.admitted && err != nil {
			t.Fatalf("a call identity issued at %v was refused at %v: %v", test.issued, now, err)
		}
		if !test.admitted {
			requireStateCode(t, err, action.ReasonReservationIndeterminate)
		}
	}
}

// Terminal calls leave the state once their call identity is past the
// admission window plus the skew; until then a replay meets the record, and
// afterwards the window rejects it. Budget usage is never pruned.
func TestRetentionPrunesTerminalCallsOnlyOutsideTheAdmissionWindow(t *testing.T) {
	fixture := admissionFixture(t)
	first := fixture.issuedCallID(t, "first")
	fixture.reserveAndRelease(t, first)
	requireStateCode(t, fixture.tryReserve(t, first), action.ReasonReservationIndeterminate)

	fixture.advance(CallIdentityAdmissionWindow + CallIdentityClockSkew - time.Second)
	fixture.reserveAndRelease(t, fixture.issuedCallID(t, "second"))
	if ids := fixture.terminalCallIDs(t); !ids[first] || len(ids) != 2 {
		t.Fatalf("terminal calls inside the window = %v", ids)
	}

	fixture.advance(2 * time.Second)
	fixture.reserveAndRelease(t, fixture.issuedCallID(t, "third"))
	ids := fixture.terminalCallIDs(t)
	if ids[first] || !ids[fixture.issuedCallID(t, "second")] || !ids[fixture.issuedCallID(t, "third")] || len(ids) != 2 {
		t.Fatalf("terminal calls after the window = %v", ids)
	}
	requireStateCode(t, fixture.tryReserve(t, first), action.ReasonReservationIndeterminate)

	status, err := fixture.store.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(status.Budgets) != 1 || status.Budgets[0].Consumed != (action.BudgetUsage{}) {
		t.Fatalf("released calls left budget usage %+v", status.Budgets)
	}
}

// Pruning never touches budget counters: committed usage of pruned calls
// stays counted.
func TestRetentionKeepsBudgetUsageOfPrunedCalls(t *testing.T) {
	fixture := admissionFixture(t)
	for _, label := range []string{"a", "b", "c"} {
		_, reserved := fixture.reserve(t, fixture.issuedCallID(t, label))
		version, err := fixture.store.MarkDispatched(context.Background(), reserved.Reservation.Identity, reserved.Snapshot.StateVersion)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := fixture.store.Settle(context.Background(), reserved.Reservation.Identity, version, OutcomeSucceeded, 0); err != nil {
			t.Fatal(err)
		}
	}
	before, err := fixture.store.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	fixture.advance(CallIdentityAdmissionWindow + CallIdentityClockSkew + time.Minute)
	fixture.reserveAndRelease(t, fixture.issuedCallID(t, "trigger"))
	after, err := fixture.store.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if after.TerminalCallCount != 1 || before.TerminalCallCount != 3 ||
		len(after.Budgets) != 1 || after.Budgets[0].Consumed != before.Budgets[0].Consumed ||
		after.Budgets[0].Consumed.CallCount != 3 {
		t.Fatalf("budgets before %+v and after pruning %+v (terminal calls %d -> %d)",
			before.Budgets, after.Budgets, before.TerminalCallCount, after.TerminalCallCount)
	}
}

// A terminal call stays while a retained approval references its
// reservation, and both leave together once the approval is prunable.
func TestRetentionKeepsTerminalCallsThatRetainedApprovalsReference(t *testing.T) {
	fixture := admissionFixture(t)
	callID := fixture.issuedCallID(t, "approved")
	input, reserved := fixture.reserve(t, callID)
	fixture.advance(10 * time.Minute)
	issued := issueFixtureApproval(t, fixture, input, reserved)
	consumed, err := consumeFixtureApproval(t, fixture, issued, actionapproval.DecisionApprove)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.store.Release(context.Background(), reserved.Reservation.Identity, consumed.StateVersion); err != nil {
		t.Fatal(err)
	}
	// The call identity is past the window, the approval request is not.
	fixture.advance(6 * time.Minute)
	fixture.reserveAndRelease(t, fixture.issuedCallID(t, "first-trigger"))
	state, _, err := fixture.store.loadState()
	if err != nil {
		t.Fatal(err)
	}
	if !fixture.terminalCallIDs(t)[callID] || approvalRecordForCallPhase(state.Approvals, callID, action.PhasePreCall) == nil {
		t.Fatal("a referenced terminal call or its approval was pruned early")
	}
	fixture.advance(CallIdentityAdmissionWindow)
	fixture.reserveAndRelease(t, fixture.issuedCallID(t, "second-trigger"))
	state, _, err = fixture.store.loadState()
	if err != nil {
		t.Fatal(err)
	}
	if fixture.terminalCallIDs(t)[callID] || approvalRecordForCallPhase(state.Approvals, callID, action.PhasePreCall) != nil {
		t.Fatal("an expired approval and its terminal call were retained")
	}
}

// writeFormatOneState persists state as a format-1 state, bypassing the
// migrating writer.
func (f *storeFixture) writeFormatOneState(t testing.TB, state State) State {
	t.Helper()
	state.FormatVersion = legacyStateFormatVersion
	digest, err := f.store.stateDigest(state)
	if err != nil {
		t.Fatal(err)
	}
	state.Digest = digest
	if err := f.store.writeBoundedJSON(f.store.statePath, state, MaxStateBytes); err != nil {
		t.Fatal(err)
	}
	return state
}

// The first write after an upgrade migrates a format-1 state: finished
// history keyed by a random call identity, which can never be admitted, is
// dropped, while history whose identity passes the MAC stays and still
// rejects its replay.
func TestFormatOneStateMigratesOnTheFirstWrite(t *testing.T) {
	fixture := admissionFixture(t)
	state, err := fixture.store.initialState()
	if err != nil {
		t.Fatal(err)
	}
	fixture.store.applyClock(&state, fixture.clock.snapshot)
	state.Revision = 1
	legacy, passing := unissuedCallID("l"), fixture.issuedCallID(t, "passing")
	for _, callID := range []string{legacy, passing} {
		state.TerminalCalls = append(state.TerminalCalls, TerminalCall{
			CallID: callID, ReservationIdentity: fixture.key.Identity(DomainBudget, []byte("legacy"), []byte(callID)),
			Outcome: OutcomeReleased, CompletedAtUnix: fixture.clock.snapshot.Time.Unix(),
		})
	}
	sort.Slice(state.TerminalCalls, func(i, j int) bool { return state.TerminalCalls[i].CallID < state.TerminalCalls[j].CallID })
	fixture.writeFormatOneState(t, state)

	status, err := fixture.store.Status(context.Background())
	if err != nil || status.TerminalCallCount != 2 {
		t.Fatalf("format-1 status = %+v, %v", status, err)
	}
	fixture.reserveAndRelease(t, fixture.issuedCallID(t, "first-write"))
	migrated, _, err := fixture.store.loadState()
	if err != nil {
		t.Fatal(err)
	}
	ids := fixture.terminalCallIDs(t)
	if migrated.FormatVersion != StateFormatVersion || ids[legacy] || !ids[passing] || len(ids) != 2 {
		t.Fatalf("migrated format %q with terminal calls %v", migrated.FormatVersion, ids)
	}
	requireStateCode(t, fixture.tryReserve(t, passing), action.ReasonReservationIndeterminate)
}

// Recovery after an upgrade completes a format-1 transaction that an older
// writer left behind; the next write migrates the recovered state.
func TestRecoveryCompletesAFormatOneTransaction(t *testing.T) {
	fixture := admissionFixture(t)
	previous, err := fixture.store.initialState()
	if err != nil {
		t.Fatal(err)
	}
	fixture.store.applyClock(&previous, fixture.clock.snapshot)
	previous.Revision = 1
	previous = fixture.writeFormatOneState(t, previous)
	after := cloneState(previous)
	after.Revision = 2
	after.TerminalCalls = []TerminalCall{{
		CallID: unissuedCallID("r"), ReservationIdentity: fixture.key.Identity(DomainBudget, []byte("recovered")),
		Outcome: OutcomeReleased, CompletedAtUnix: fixture.clock.snapshot.Time.Unix(),
	}}
	after.Digest, err = fixture.store.stateDigest(after)
	if err != nil {
		t.Fatal(err)
	}
	transaction := stateTransaction{
		Schema: TransactionSchema, FormatVersion: legacyStateFormatVersion,
		BeforePersisted: true, BeforeRevision: previous.Revision, BeforeDigest: previous.Digest, After: after,
	}
	transaction.Digest, err = fixture.store.transactionDigest(transaction)
	if err != nil {
		t.Fatal(err)
	}
	body, err := encodeBoundedCompactJSON(transaction, MaxStateTransaction)
	if err != nil {
		t.Fatal(err)
	}
	if err := fixture.store.publish(fixture.store.transactionPath, body); err != nil {
		t.Fatal(err)
	}
	if version, err := fixture.store.CurrentStateVersion(context.Background()); err != nil || version != after.Digest {
		t.Fatalf("recovered version = %q, %v; want %q", version, err, after.Digest)
	}
	fixture.reserveAndRelease(t, fixture.issuedCallID(t, "after-recovery"))
	migrated, _, err := fixture.store.loadState()
	if err != nil {
		t.Fatal(err)
	}
	if migrated.FormatVersion != StateFormatVersion || fixture.terminalCallIDs(t)[unissuedCallID("r")] {
		t.Fatalf("recovered state was not migrated: format %q", migrated.FormatVersion)
	}
}

// Near the state size limit new calls are refused with room left to finalize
// the active ones.
func TestAdmissionKeepsHeadroomToFinalizeActiveReservations(t *testing.T) {
	fixture := admissionFixture(t)
	_, active := fixture.reserve(t, fixture.issuedCallID(t, "active"))
	previous, persisted, err := fixture.store.loadState()
	if err != nil {
		t.Fatal(err)
	}
	next := cloneState(previous)
	base, err := encodeBoundedJSON(next, MaxStateBytes)
	if err != nil {
		t.Fatal(err)
	}
	record := func(index int) TerminalCall {
		callID := orderedCapacityCallID(index)
		return TerminalCall{
			CallID: callID, ReservationIdentity: fixture.key.Identity(DomainBudget, []byte("headroom"), []byte(callID)),
			Outcome: OutcomeReleased, CompletedAtUnix: fixture.clock.snapshot.Time.Unix(),
		}
	}
	next.TerminalCalls = []TerminalCall{record(0)}
	one, err := encodeBoundedJSON(next, MaxStateBytes)
	if err != nil {
		t.Fatal(err)
	}
	target := MaxStateBytes - StateAdmissionHeadroomBytes + StateAdmissionHeadroomBytes/4
	count := (target - len(base)) / (len(one) - len(base))
	next.TerminalCalls = make([]TerminalCall, count)
	for index := range next.TerminalCalls {
		next.TerminalCalls[index] = record(index)
	}
	if err := fixture.store.writeState(previous, persisted, &next); err != nil {
		t.Fatal(err)
	}
	err = fixture.tryReserve(t, fixture.issuedCallID(t, "refused"))
	requireStateCode(t, err, action.ReasonStateUnavailable)
	if !strings.Contains(err.Error(), "too close to its size limit") {
		t.Fatalf("refusal = %v", err)
	}
	if _, err := fixture.store.Release(context.Background(), active.Reservation.Identity, next.Digest); err != nil {
		t.Fatalf("an active reservation could not be finalized near the limit: %v", err)
	}
}

// The byte check accepts exactly the keyed identities action defines under
// the given key ID.
func TestIdentityUsesKeyMatchesTheKeyedIdentityContract(t *testing.T) {
	hex64 := strings.Repeat("0123456789abcdef", 4)
	keyIDs := []string{"0123456789abcdef0123456789abcdef", "a", "a-b", "-a", "a-", "A1", "a_b", strings.Repeat("a", 33), ""}
	tails := []string{hex64, hex64[:63], hex64 + "0", strings.ToUpper(hex64), hex64[:63] + "g", ""}
	for _, keyID := range keyIDs {
		for _, otherKey := range keyIDs {
			for _, tail := range tails {
				for _, identity := range []string{
					"hmac-sha256:v1:" + otherKey + ":" + tail,
					"hmac-sha256:v2:" + otherKey + ":" + tail,
					"hmac-sha256:v1:" + otherKey + ":" + tail + "\n",
				} {
					want := action.ValidKeyedIdentity(identity) && strings.HasPrefix(identity, "hmac-sha256:v1:"+keyID+":")
					if got := identityUsesKey(identity, keyID); got != want {
						t.Fatalf("identityUsesKey(%q, %q) = %t, want %t", identity, keyID, got, want)
					}
				}
			}
		}
	}
}

// Over 100,000 calls at a steady rate, retained terminal history stays
// bounded by the calls issued within the admission window and far below the
// state size limit.
func TestRetentionBoundsHistoryOverOneHundredThousandCalls(t *testing.T) {
	const (
		calls          = 100_000
		callsPerSecond = 5
		pruneEvery     = 50
	)
	start := time.Date(2026, 10, 21, 0, 0, 0, 0, time.UTC)
	state := State{Reservations: []Reservation{}, Approvals: []ApprovalRecord{}, TerminalCalls: []TerminalCall{}}
	maximum := 0
	var now time.Time
	for index := range calls {
		now = start.Add(time.Duration(index) * time.Second / callsPerSecond)
		var raw [callIdentityBytes]byte
		binary.BigEndian.PutUint64(raw[:8], uint64(now.Unix())<<24)
		binary.BigEndian.PutUint32(raw[callIdentitySeconds:], uint32(index))
		callID := encodeCallIdentity(raw)
		state.TerminalCalls = append(state.TerminalCalls, TerminalCall{
			CallID: callID, ReservationIdentity: "hmac-sha256:v1:" + strings.Repeat("a", 32) + ":" + strings.Repeat("b", 64),
			Outcome: OutcomeSucceeded, CompletedAtUnix: now.Unix(),
		})
		if index%pruneEvery == 0 {
			pruneRetainedHistory(&state, now)
		}
		maximum = max(maximum, len(state.TerminalCalls))
	}
	pruneRetainedHistory(&state, now)
	window := int((CallIdentityAdmissionWindow + CallIdentityClockSkew).Seconds()) + 1
	if bound := callsPerSecond*window + pruneEvery; maximum > bound || len(state.TerminalCalls) > callsPerSecond*window {
		t.Fatalf("retained %d terminal calls at most and %d at the end, bound %d", maximum, len(state.TerminalCalls), bound)
	}
	body, err := encodeBoundedJSON(state, MaxStateBytes)
	if err != nil || len(body) > MaxStateBytes-StateAdmissionHeadroomBytes {
		t.Fatalf("retained history encodes to %d bytes: %v", len(body), err)
	}
}
