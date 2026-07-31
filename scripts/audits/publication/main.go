package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"time"
)

func main() {
	if err := runCLI(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "publication-audit:", err)
		os.Exit(1)
	}
}

func runCLI(args []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("publication-audit", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	options := defaultAuditOptions()
	flags.StringVar(&options.Root, "root", ".", "repository root")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("usage: publication-audit [--root PATH]")
	}
	// The audit scans every post-boundary blob. Keep a hard deadline, but leave
	// enough headroom for race-instrumented and resource-constrained CI runners.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	report, err := auditRepository(ctx, options)
	if err != nil {
		return err
	}
	if len(report.Findings) > 0 {
		for _, finding := range report.Findings {
			location := finding.Path
			if finding.Line > 0 {
				location = fmt.Sprintf("%s:%d", location, finding.Line)
			}
			fmt.Fprintf(stderr, "%s [%s]: %s\n", location, finding.Rule, finding.Detail)
		}
		return fmt.Errorf("%d publication boundary finding(s)", len(report.Findings))
	}
	fmt.Fprintf(
		stdout,
		"publication-audit: ok (%d tracked files, %d post-boundary commits, %d post-boundary blobs)\n",
		report.TrackedFiles,
		report.AuditedCommits,
		report.AuditedHistoricalBlobs,
	)
	return nil
}
