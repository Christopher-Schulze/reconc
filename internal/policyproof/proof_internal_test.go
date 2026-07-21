package policyproof

import (
	"path/filepath"
	"strings"
	"testing"

	"reconc.dev/reconc/internal/policy"
	"reconc.dev/reconc/internal/runtime"
)

func TestValidateRecordRejectsStoredNonBlockingDecision(t *testing.T) {
	repo := t.TempDir()
	report := runtime.NewEmptyReport(repo, filepath.Join(repo, ".reconc", "policy.lock.json"), policy.ModeBlock, runtime.Empty())
	report.Finalize()
	record, err := newRecord(repo, "check", strings.Repeat("a", 64), &report)
	if err != nil {
		t.Fatal(err)
	}
	if err := validateRecord(record, repo); err == nil || !strings.Contains(err.Error(), "not an unresolved block") {
		t.Fatalf("stored non-blocking receipt was accepted: %v", err)
	}
}
