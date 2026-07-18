// Package main preserves the former operator-facing compatibility utility.
// Current bootstraps use `reconc prune`; this utility exists for two cases:
//
//  1. Inspect: `reconc-prune --dry-run` shows what *would* be removed.
//  2. Force: `reconc-prune --force` ignores the interval and runs now (for
//     after vacations, or when investigating disk usage).
//
// Exit code is always 0 unless flag parsing fails. Internal errors are
// printed to stderr but do not abort.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"

	"reconc-harness/template/audits/lib/prune"
)

func main() {
	dryRun := flag.Bool("dry-run", false, "report what would be deleted without touching anything")
	force := flag.Bool("force", false, "compatibility flag; this utility always prunes immediately")
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr,
			"usage: reconc-prune [--dry-run] [--force]\n\n"+
				"Trims Reconc state to the budget defined in\n"+
				"tools/reconc/harness/template/config/workflow/prune-policy.yaml.\n"+
				"Prefer the product command: reconc prune . [--dry-run] [--json].\n")
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
	printReport(os.Stdout, os.Stderr, report, *dryRun)
}

func printReport(stdout, stderr io.Writer, r prune.Report, dryRun bool) {
	prefix := "pruned"
	if dryRun {
		prefix = "would prune"
	}
	fmt.Fprintf(stdout, "%s sessions: kept=%d deleted=%d\n", prefix, r.SessionsKept, r.SessionsDeleted)
	fmt.Fprintf(stdout, "%s reports:  kept=%d deleted=%d\n", prefix, r.ReportsKept, r.ReportsDeleted)
	fmt.Fprintf(stdout, "%s command proofs: kept=%d deleted=%d\n", prefix, r.CommandProofsKept, r.CommandProofsDeleted)
	fmt.Fprintf(stdout, "%s stale locks: deleted=%d\n", prefix, r.LocksDeleted)
	fmt.Fprintf(stdout, "%s audit.jsonl: lines_dropped=%d bytes_freed=%d\n", prefix, r.JsonlLinesDropped, r.JsonlBytesFreed)
	if len(r.Errors) > 0 {
		fmt.Fprintln(stderr, "errors during prune:")
		for _, e := range r.Errors {
			fmt.Fprintf(stderr, "  - %s\n", e)
		}
	}
}
