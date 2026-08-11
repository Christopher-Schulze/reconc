package actionledger

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"reconc.dev/reconc/internal/action"
)

func TestTailStatsAndRenderUseVerifiedBoundedEvidence(t *testing.T) {
	fixture := newLedgerStoreFixture(t)
	records := successfulBudgetLifecycle()
	for _, record := range records {
		fixture.appendRecord(t, record)
	}
	tail, err := fixture.store.Tail(context.Background(), Filter{
		RunIdentity: fixture.identity("run"), Event: EventPreDecision,
	}, 1)
	if err != nil {
		t.Fatal(err)
	}
	if tail.FormatVersion != TailReportFormat || len(tail.Records) != 1 ||
		tail.Records[0].Event != EventPreDecision || tail.Verification.Integrity != StatusVerified {
		t.Fatalf("Tail() = %#v", tail)
	}
	tailJSON, err := MarshalTail(tail)
	if err != nil {
		t.Fatal(err)
	}
	tailText := RenderTailText(tail)
	if !bytes.HasSuffix(tailJSON, []byte("\n")) ||
		bytes.Contains(tailJSON, []byte(`"arguments":`)) ||
		!bytes.Contains(tailText, []byte("retained=1..9 dropped_history=false")) ||
		!bytes.Contains(tailText, []byte("decision=allow")) {
		t.Fatalf("tail rendering is invalid: %s", tailJSON)
	}
	stats, err := fixture.store.Stats(context.Background(), Filter{Decision: action.DecisionAllow})
	if err != nil {
		t.Fatal(err)
	}
	if stats.Counts.Calls != 1 || stats.Counts.Evaluated != 1 || stats.Counts.Allowed != 1 ||
		stats.Counts.DownstreamSucceeded != 1 || stats.Counts.Delivered != 1 ||
		stats.Counts.TerminalComplete != 1 || stats.Counts.EvidenceComplete != 1 ||
		len(stats.ByRun) != 1 || len(stats.BySession) != 1 || len(stats.ByPrincipal) != 1 || len(stats.ByTool) != 1 {
		t.Fatalf("Stats() = %#v", stats)
	}
	firstJSON, err := MarshalStats(stats)
	if err != nil {
		t.Fatal(err)
	}
	secondJSON, err := MarshalStats(stats)
	if err != nil || !bytes.Equal(firstJSON, secondJSON) {
		t.Fatalf("stats JSON is not deterministic: %v", err)
	}
	if text := string(RenderStatsText(stats)); !strings.Contains(text, "retained=1..9 dropped_history=false") ||
		!strings.Contains(text, "calls=1") ||
		!strings.Contains(text, "succeeded=1") {
		t.Fatalf("stats text = %q", text)
	}
	verificationJSON, err := MarshalVerification(tail.Verification)
	if err != nil || !bytes.Contains(verificationJSON, []byte(`"integrity": "verified"`)) ||
		!bytes.Contains(RenderVerificationText(tail.Verification), []byte("sequence: retained=1..9")) {
		t.Fatalf("verification rendering = %s, %v", verificationJSON, err)
	}
}

func TestFilterAndTailLimitsFailClosed(t *testing.T) {
	validIdentity := testKeyedIdentity("a")
	tests := []Filter{
		{CallID: "act_invalid"},
		{RunIdentity: "run"},
		{SessionIdentity: "session"},
		{Principal: "contains space"},
		{ToolIdentity: "tool\nname"},
		{Event: "unknown"},
		{Decision: "unknown"},
		{Since: time.Unix(-1, 0)},
	}
	for _, filter := range tests {
		if err := filter.Validate(); err == nil {
			t.Fatalf("Filter.Validate(%#v) succeeded", filter)
		}
	}
	if err := (Filter{RunIdentity: validIdentity, SessionIdentity: validIdentity}).Validate(); err != nil {
		t.Fatalf("valid filter rejected: %v", err)
	}
	if err := (Filter{ToolIdentity: "declaration_id:database-write"}).Validate(); err != nil {
		t.Fatalf("valid tool filter rejected: %v", err)
	}
	farFuture, err := time.Parse(time.RFC3339, "9999-12-31T23:59:59Z")
	if err != nil {
		t.Fatal(err)
	}
	if err := (Filter{Since: farFuture}).Validate(); err != nil {
		t.Fatalf("far-future RFC3339 filter rejected: %v", err)
	}
	fixture := newLedgerStoreFixture(t)
	for _, limit := range []int{0, -1, MaxTailRecords + 1} {
		if _, err := fixture.store.Tail(context.Background(), Filter{}, limit); err == nil {
			t.Fatalf("Tail limit %d succeeded", limit)
		}
	}
}

func TestStatsKeepsToolIdentityModesDistinct(t *testing.T) {
	fixture := newLedgerStoreFixture(t)
	tools := []ToolIdentity{
		{Mode: action.LedgerDeclarationID, Value: "database-write"},
		{Mode: action.LedgerExactName, Value: "database-write"},
	}
	for index, tool := range tools {
		callID := "act_" + strings.Repeat(string(rune('a'+index)), 26)
		request := testLedgerRecord(EventRequestAccepted)
		request.Call.CallID = callID
		request.Call.Tool = tool
		fixture.appendRecord(t, request)
		decision := testLedgerRecord(EventPreDecision)
		decision.Call.CallID = callID
		decision.Call.Tool = tool
		decision.Decision.Decision = action.DecisionBlock
		decision.Decision.Reason = action.ReasonRuleMatched
		decision.PreDecision.Outcome = action.OutcomeDispatchBlocked
		fixture.appendRecord(t, decision)
	}
	stats, err := fixture.store.Stats(context.Background(), Filter{})
	if err != nil || len(stats.ByTool) != 2 {
		t.Fatalf("tool groups = %#v, %v", stats.ByTool, err)
	}
	filtered, err := fixture.store.Stats(context.Background(), Filter{
		ToolIdentity: "exact_name:database-write",
	})
	if err != nil || filtered.Counts.Calls != 1 || filtered.Calls[0].Tool.Mode != action.LedgerExactName {
		t.Fatalf("exact tool filter = %#v, %v", filtered, err)
	}
}

func TestEmptyReportsHaveCanonicalNonNullCollections(t *testing.T) {
	tail, stats, verification := EmptyTailReport(), EmptyStatsReport(), EmptyVerificationReport()
	if _, err := MarshalTail(tail); err != nil {
		t.Fatal(err)
	}
	if _, err := MarshalStats(stats); err != nil {
		t.Fatal(err)
	}
	if _, err := MarshalVerification(verification); err != nil {
		t.Fatal(err)
	}
	if tail.Records == nil || stats.Calls == nil || stats.ByRun == nil ||
		stats.BySession == nil || stats.ByPrincipal == nil || stats.ByTool == nil {
		t.Fatal("empty report contains a null collection")
	}
	if text := string(RenderTailText(tail)); !strings.Contains(text, "retained=empty dropped_history=false") {
		t.Fatalf("empty tail text = %q", text)
	}
	if text := string(RenderStatsText(stats)); !strings.Contains(text, "retained=empty dropped_history=false") {
		t.Fatalf("empty stats text = %q", text)
	}
}

func TestTextReportsExposeDroppedRetainedHistoryBoundary(t *testing.T) {
	verification := VerificationReport{
		FormatVersion: FormatVersion, Integrity: StatusVerified,
		ArchiveContinuity: StatusVerified, DetachedHead: HeadMatched,
		RecordCount: 5, ArchiveCount: MaxArchives,
		FirstRetainedSequence: 10, LastRetainedSequence: 14,
		FirstRecordedSequence: 1, DroppedHistory: true, DroppedBeforeSequence: 10,
		EventsEvaluated: true, EventsComplete: true,
		CallsEvaluated: true, CallsComplete: true,
	}
	tail := TailReport{FormatVersion: TailReportFormat, Verification: verification, Records: []Record{}}
	stats := StatsReport{
		FormatVersion: StatsReportFormat, Verification: verification, Calls: []CallStatus{},
		ByRun: []LifecycleGroup{}, BySession: []LifecycleGroup{},
		ByPrincipal: []LifecycleGroup{}, ByTool: []LifecycleGroup{},
	}
	if _, err := MarshalTail(tail); err != nil {
		t.Fatal(err)
	}
	if _, err := MarshalStats(stats); err != nil {
		t.Fatal(err)
	}
	for _, rendered := range []struct {
		name string
		text string
	}{
		{name: "tail", text: string(RenderTailText(tail))},
		{name: "stats", text: string(RenderStatsText(stats))},
	} {
		if !strings.Contains(rendered.text, "retained=10..14 dropped_history=true dropped_before=10") {
			t.Fatalf("%s text omits retained-history boundary: %q", rendered.name, rendered.text)
		}
	}
}

func TestReportMarshalRejectsMutatedLedgerClaims(t *testing.T) {
	fixture := newLedgerStoreFixture(t)
	for _, record := range successfulBudgetLifecycle() {
		fixture.appendRecord(t, record)
	}
	tail, err := fixture.store.Tail(context.Background(), Filter{}, MaxTailRecords)
	if err != nil {
		t.Fatal(err)
	}
	tail.Records[0].Call.Principal = "unsafe principal"
	if _, err := MarshalTail(tail); err == nil {
		t.Fatal("MarshalTail() accepted a mutated retained record")
	}
	stats, err := fixture.store.Stats(context.Background(), Filter{})
	if err != nil {
		t.Fatal(err)
	}
	stats.Counts.Calls++
	if _, err := MarshalStats(stats); err == nil {
		t.Fatal("MarshalStats() accepted counts that disagree with call statuses")
	}
	stats, err = fixture.store.Stats(context.Background(), Filter{})
	if err != nil {
		t.Fatal(err)
	}
	stats.ByPrincipal[0].Identity = "invented"
	if _, err := MarshalStats(stats); err == nil {
		t.Fatal("MarshalStats() accepted a mutated lifecycle group")
	}
	verification := tail.Verification
	verification.RecordCount++
	if _, err := MarshalVerification(verification); err == nil {
		t.Fatal("MarshalVerification() accepted a contradictory verified record count")
	}
	invalid := VerificationReport{
		FormatVersion: FormatVersion, Integrity: StatusInvalid,
		ArchiveContinuity: StatusInvalid, DetachedHead: HeadInvalid,
		EventsComplete: true, CallsComplete: true,
	}
	if _, err := MarshalVerification(invalid); err == nil {
		t.Fatal("MarshalVerification() accepted unevaluated completeness claims")
	}
}
