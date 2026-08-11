package actionledger

import (
	"context"
	"os"
	"strings"
	"testing"

	"reconc.dev/reconc/internal/action"
)

func FuzzVerifyTamperedDetachedHead(f *testing.F) {
	f.Add([]byte("{}\n"))
	f.Add([]byte("not-json\n"))
	valid, err := encodeChainHead(chainHead{
		Schema: headSchema, FormatVersion: FormatVersion, ChainVersion: ChainVersion,
		RepositoryIdentity: testKeyedIdentity("2"), FirstSequence: 1,
		FirstDigest: strings.Repeat("a", 64), LastSequence: 1,
		LastDigest: strings.Repeat("a", 64), EntryCount: 1,
		UpdatedAt: "2026-08-11T12:00:00Z",
	})
	if err != nil {
		f.Fatal(err)
	}
	f.Add(valid)
	f.Fuzz(func(t *testing.T, input []byte) {
		if len(input) > maxHeadBytes+1 {
			input = input[:maxHeadBytes+1]
		}
		fixture := newLedgerStoreFixture(t)
		fixture.append(t, EventRequestAccepted)
		decision := fixture.record(EventPreDecision)
		decision.Decision.Decision = action.DecisionBlock
		decision.Decision.Reason = action.ReasonRuleMatched
		decision.PreDecision.Outcome = action.OutcomeDispatchBlocked
		fixture.appendRecord(t, decision)
		if err := os.WriteFile(fixture.store.headPath, input, 0o600); err != nil {
			t.Fatal(err)
		}
		_, _ = fixture.store.Verify(context.Background())
	})
}
