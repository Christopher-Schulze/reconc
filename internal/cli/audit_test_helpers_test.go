package cli

import (
	"testing"

	"reconc.dev/reconc/internal/audit"
)

type cliAuditRecord struct {
	timestamp      string
	event          string
	decision       string
	ok             bool
	ruleIDs        []string
	violationCount int
	blockingCount  int
	writePaths     []string
}

func appendCLIAuditRecords(t *testing.T, repoRoot string, records ...cliAuditRecord) {
	t.Helper()
	for _, record := range records {
		err := audit.Append(repoRoot, audit.Entry{
			Timestamp:      record.timestamp,
			Event:          record.event,
			Decision:       record.decision,
			OK:             record.ok,
			RuleIDs:        record.ruleIDs,
			ViolationCount: record.violationCount,
			BlockingCount:  record.blockingCount,
			WritePaths:     record.writePaths,
		}, audit.DefaultMaxSizeBytes)
		if err != nil {
			t.Fatalf("append chained audit fixture: %v", err)
		}
	}
}
