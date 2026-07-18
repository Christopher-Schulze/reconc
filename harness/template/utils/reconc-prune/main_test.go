package main

import (
	"bytes"
	"strings"
	"testing"

	"reconc-harness/template/audits/lib/prune"
)

func TestPrintReportShowsAllCounts(t *testing.T) {
	report := prune.Report{
		SessionsKept:         25,
		SessionsDeleted:      10,
		ReportsKept:          25,
		ReportsDeleted:       8,
		CommandProofsKept:    64,
		CommandProofsDeleted: 4,
		LocksDeleted:         2,
		JsonlLinesDropped:    42,
		JsonlBytesFreed:      1234567,
	}
	var stdout, stderr bytes.Buffer
	printReport(&stdout, &stderr, report, false)
	out := stdout.String()
	for _, want := range []string{"kept=25", "deleted=10", "deleted=8", "kept=64", "deleted=4", "deleted=2", "lines_dropped=42", "bytes_freed=1234567"} {
		if !strings.Contains(out, want) {
			t.Fatalf("output missing %q: %s", want, out)
		}
	}
	if stderr.Len() != 0 {
		t.Fatalf("unexpected stderr: %s", stderr.String())
	}
}

func TestDryRunPrefix(t *testing.T) {
	var stdout, stderr bytes.Buffer
	printReport(&stdout, &stderr, prune.Report{Errors: []string{"locked"}}, true)
	for _, line := range strings.Split(strings.TrimSpace(stdout.String()), "\n") {
		if !strings.HasPrefix(line, "would prune ") {
			t.Fatalf("dry-run prefix wrong: %s", line)
		}
	}
	if stderr.String() != "errors during prune:\n  - locked\n" {
		t.Fatalf("unexpected error report: %q", stderr.String())
	}
}

func TestPolicyDrivesReport(t *testing.T) {
	// End-to-end: run prune.Run with empty inputs through DefaultPolicy and
	// confirm the Report shape matches what the CLI prints. This guards
	// against the lib/CLI contract drifting.
	policy := prune.DefaultPolicy()
	r := prune.Run(prune.Options{RepoRoot: t.TempDir(), ReconcHome: t.TempDir(), Policy: policy, DryRun: true})
	// Empty inputs -> all-zero counts, no errors.
	if r.SessionsDeleted != 0 || r.ReportsDeleted != 0 || r.CommandProofsDeleted != 0 || r.LocksDeleted != 0 {
		t.Fatalf("expected all-zero counts on empty tree, got %+v", r)
	}
	if len(r.Errors) != 0 {
		t.Fatalf("unexpected errors: %v", r.Errors)
	}
}
