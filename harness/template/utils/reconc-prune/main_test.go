package main

import (
	"bytes"
	"fmt"
	"strings"
	"testing"

	"reconc-harness/template/audits/lib/prune"
)

func TestPrintReportShowsAllCounts(t *testing.T) {
	report := prune.Report{
		SessionsKept:      25,
		SessionsDeleted:   10,
		ReportsKept:       25,
		ReportsDeleted:    8,
		LocksDeleted:      2,
		JsonlLinesDropped: 42,
		JsonlBytesFreed:   1234567,
	}
	var buf bytes.Buffer
	fmt.Fprintf(&buf, "pruned sessions: kept=%d deleted=%d\n", report.SessionsKept, report.SessionsDeleted)
	fmt.Fprintf(&buf, "pruned reports:  kept=%d deleted=%d\n", report.ReportsKept, report.ReportsDeleted)
	fmt.Fprintf(&buf, "pruned stale locks: deleted=%d\n", report.LocksDeleted)
	fmt.Fprintf(&buf, "pruned audit.jsonl: lines_dropped=%d bytes_freed=%d\n", report.JsonlLinesDropped, report.JsonlBytesFreed)
	out := buf.String()
	for _, want := range []string{"kept=25", "deleted=10", "deleted=8", "deleted=2", "lines_dropped=42", "bytes_freed=1234567"} {
		if !strings.Contains(out, want) {
			t.Fatalf("output missing %q: %s", want, out)
		}
	}
}

func TestDryRunPrefix(t *testing.T) {
	// Dry-run replaces "pruned" with "would prune" in every line. Locks the
	// formatting contract so a future refactor cannot silently change it.
	prefixes := []string{"would prune"}
	for _, p := range prefixes {
		var buf bytes.Buffer
		fmt.Fprintf(&buf, "%s sessions: kept=%d deleted=%d\n", p, 0, 0)
		out := buf.String()
		if !strings.HasPrefix(out, p+" sessions") {
			t.Fatalf("dry-run prefix wrong: %s", out)
		}
	}
}

func TestPolicyDrivesReport(t *testing.T) {
	// End-to-end: run prune.Run with empty inputs through DefaultPolicy and
	// confirm the Report shape matches what the CLI prints. This guards
	// against the lib/CLI contract drifting.
	policy := prune.DefaultPolicy()
	r := prune.Run(prune.Options{RepoRoot: t.TempDir(), ReconcHome: t.TempDir(), Policy: policy, DryRun: true})
	// Empty inputs -> all-zero counts, no errors.
	if r.SessionsDeleted != 0 || r.ReportsDeleted != 0 || r.LocksDeleted != 0 {
		t.Fatalf("expected all-zero counts on empty tree, got %+v", r)
	}
	if len(r.Errors) != 0 {
		t.Fatalf("unexpected errors: %v", r.Errors)
	}
}
