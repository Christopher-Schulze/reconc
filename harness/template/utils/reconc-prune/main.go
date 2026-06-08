// Package main is the operator-facing CLI for the prune library.
//
// The auto-trigger in tools/reconc/harness/template/audits/audit_cache.go calls prune
// every prune_interval_seconds (default 7d) without any manual action. This
// CLI exists for two cases:
//
//  1. Inspect: `reconc-prune --dry-run` shows what *would* be removed.
//  2. Force: `reconc-prune --force` ignores the interval and runs now (for
//     after vacations, or when investigating disk usage).
//
// Exit code is always 0 unless flag parsing fails. Internal errors are
// printed to stderr but do not abort, matching the auto-trigger's
// fail-safe semantics.
package main

import (
	"flag"
	"fmt"
	"os"

	"reconc-harness/template/audits/lib/prune"
)

func main() {
	dryRun := flag.Bool("dry-run", false, "report what would be deleted without touching anything")
	force := flag.Bool("force", false, "kept for symmetry; this CLI always prunes immediately (the auto-trigger is the rate-limited path)")
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr,
			"usage: reconc-prune [--dry-run] [--force]\n\n"+
				"Trims Reconc state to the budget defined in\n"+
				"tools/reconc/harness/template/config/workflow/prune-policy.yaml. The auto-trigger in\n"+
				"audit_cache.go calls this same library once per prune_interval_seconds.\n")
	}
	flag.Parse()
	_ = *force
	root, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "reconc-prune: get cwd: %v\n", err)
		os.Exit(2)
	}
	policy, err := prune.LoadPolicy(prune.PolicyPathFromRepo(root))
	if err != nil {
		fmt.Fprintf(os.Stderr, "reconc-prune: load policy: %v (using DefaultPolicy)\n", err)
	}
	report := prune.Run(prune.Options{RepoRoot: root, Policy: policy, DryRun: *dryRun})
	printReport(report, *dryRun)
}

func printReport(r prune.Report, dryRun bool) {
	prefix := "pruned"
	if dryRun {
		prefix = "would prune"
	}
	fmt.Printf("%s sessions: kept=%d deleted=%d\n", prefix, r.SessionsKept, r.SessionsDeleted)
	fmt.Printf("%s reports:  kept=%d deleted=%d\n", prefix, r.ReportsKept, r.ReportsDeleted)
	fmt.Printf("%s stale locks: deleted=%d\n", prefix, r.LocksDeleted)
	fmt.Printf("%s audit.jsonl: lines_dropped=%d bytes_freed=%d\n", prefix, r.JsonlLinesDropped, r.JsonlBytesFreed)
	if len(r.Errors) > 0 {
		fmt.Fprintf(os.Stderr, "errors during prune:\n")
		for _, e := range r.Errors {
			fmt.Fprintf(os.Stderr, "  - %s\n", e)
		}
	}
}
