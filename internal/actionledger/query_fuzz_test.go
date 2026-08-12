package actionledger

import (
	"os"
	"testing"
	"time"

	"reconc.dev/reconc/internal/action"
)

func FuzzFilterValidateAndMatch(f *testing.F) {
	f.Add("act_aaaaaaaaaaaaaaaaaaaaaaaaaa", "operator", "declaration_id:database-write", "pre_decision", "block", int64(1))
	f.Add("bad", "contains space", "tool\nname", "invented", "invented", int64(-1))
	record := testLedgerRecord(EventPreDecision)
	f.Fuzz(func(t *testing.T, callID, principal, tool, event, decision string, since int64) {
		filter := Filter{
			CallID: callID, Principal: principal, ToolIdentity: tool,
			Event: EventType(event), Decision: action.Decision(decision), Since: time.Unix(since, 0),
		}
		if err := filter.Validate(); err == nil {
			_ = filter.Matches(record)
		}
	})
}

func FuzzMalformedArchiveSets(f *testing.F) {
	f.Add(uint8(1), []byte("{}\n"))
	f.Add(uint8(6), []byte("not-json\n"))
	fixture := newLedgerStoreFixture(f)
	f.Fuzz(func(t *testing.T, presence uint8, input []byte) {
		if len(input) > MaxRecordBytes {
			input = input[:MaxRecordBytes]
		}
		for index := 0; index <= MaxArchives; index++ {
			path := fixture.store.livePath
			if index > 0 {
				path += "." + string(rune('0'+index))
			}
			if presence&(1<<index) == 0 {
				if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
					t.Fatal(err)
				}
				continue
			}
			writePrivateLedgerTestFile(t, fixture, path, input)
		}
		_ = fixture.store.validateArchiveSet()
		_, _ = fixture.store.loadRecordsLocked()
	})
}
