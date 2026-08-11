package retention

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestProjectRetentionPreservesLedgerArchivesHeadAndActiveTransaction(t *testing.T) {
	repository := t.TempDir()
	stateRoot := t.TempDir()
	now := time.Now().UTC()
	policy := DefaultPolicy()
	policy.ProjectRoots = ClassPolicy{MaxFiles: 1, MaxBytes: 1, MaxAge: time.Hour}
	current := ProjectDir(stateRoot, repository)
	writeTimed(t, filepath.Join(current, "current"), []byte("current"), now)
	ledgerProject := filepath.Join(stateRoot, "projects", "1111111111111111")
	actionDirectory := filepath.Join(ledgerProject, "action")
	for name, body := range map[string][]byte{
		"ledger.jsonl":                []byte("live\n"),
		"ledger.jsonl.1":              []byte("archive-one\n"),
		"ledger.jsonl.2":              []byte("archive-two\n"),
		"ledger.head.json":            []byte("head\n"),
		"ledger-transaction.json":     []byte("transaction\n"),
		"ledger-transaction-backup.0": []byte("backup\n"),
	} {
		writeTimed(t, filepath.Join(actionDirectory, name), body, now.Add(-365*24*time.Hour))
	}
	stale := filepath.Join(stateRoot, "projects", "2222222222222222")
	writeTimed(t, filepath.Join(stale, "stale"), []byte("stale"), now.Add(-365*24*time.Hour))

	report := Run(Options{
		RepoRoot: repository, StateRoot: stateRoot, Policy: policy,
		Now: now, TempRoot: t.TempDir(),
	})
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Fatalf("unprotected stale root survived: %v", err)
	}
	for _, name := range []string{
		"ledger.jsonl", "ledger.jsonl.1", "ledger.jsonl.2", "ledger.head.json",
		"ledger-transaction.json", "ledger-transaction-backup.0",
	} {
		if _, err := os.Stat(filepath.Join(actionDirectory, name)); err != nil {
			t.Fatalf("protected ledger state %s was removed: %v", name, err)
		}
	}
	if !strings.Contains(strings.Join(report.Errors, "\n"), "protected project state uses") {
		t.Fatalf("over-budget protected ledger was not reported: %+v", report)
	}
}
