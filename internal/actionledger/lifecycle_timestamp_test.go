package actionledger

import (
	"strings"
	"testing"
)

func TestBuildCallStatusesRejectsMalformedTimestamp(t *testing.T) {
	record := testLedgerRecord(EventRequestAccepted)
	record.Timestamp = "not-a-timestamp"

	_, err := BuildCallStatuses([]Record{record})
	if err == nil || !strings.Contains(err.Error(), "invalid timestamp") {
		t.Fatalf("BuildCallStatuses() error = %v, want invalid timestamp", err)
	}
}
