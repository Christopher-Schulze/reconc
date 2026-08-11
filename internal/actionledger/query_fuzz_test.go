package actionledger

import (
	"os"
	"path/filepath"
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
	f.Fuzz(func(t *testing.T, presence uint8, input []byte) {
		if len(input) > MaxRecordBytes {
			input = input[:MaxRecordBytes]
		}
		directory := t.TempDir()
		live := filepath.Join(directory, liveFileName)
		for index := 0; index <= MaxArchives; index++ {
			if presence&(1<<index) == 0 {
				continue
			}
			path := live
			if index > 0 {
				path = live + "." + string(rune('0'+index))
			}
			if err := os.WriteFile(path, input, 0o600); err != nil {
				t.Fatal(err)
			}
		}
		store := &Store{directory: directory, livePath: live}
		_ = store.validateArchiveSet()
		_, _ = store.loadRecordsLocked()
	})
}
