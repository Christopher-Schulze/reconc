package cli

import (
	"fmt"
	"os"
	"reconc.dev/reconc/internal/audit"
	"reconc.dev/reconc/internal/runtime"
	"time"
)

// auditEntryFromReport builds an audit.Entry from a finished
// runtime.CheckReport + the original ExecutionInputs. Captures what
// the evaluator saw and what it decided. Called by runCheck / runCI /
// runAssert only when auditing is enabled.
func auditEntryFromReport(event string, report *runtime.CheckReport, reconcVersion string, start time.Time) audit.Entry {
	ruleIDs := make([]string, 0, len(report.Violations))
	for _, v := range report.Violations {
		ruleIDs = append(ruleIDs, v.RuleID)
	}
	return audit.Entry{
		Event:          event,
		Decision:       string(report.Decision),
		OK:             report.OK,
		RuleIDs:        ruleIDs,
		ViolationCount: report.ViolationCount,
		BlockingCount:  report.BlockingViolationCount,
		WritePaths:     report.Inputs.WritePaths,
		ReadPaths:      report.Inputs.ReadPaths,
		Commands:       report.Inputs.Commands,
		Claims:         report.Inputs.Claims,
		RepoRoot:       report.RepoRoot,
		ReconcVersion:  reconcVersion,
		DurationMs:     time.Since(start).Milliseconds(),
	}
}

// maybeAudit appends one entry to the audit log when logging is
// enabled for the given repo. Non-fatal on error: audit is best-effort
// and must never break the check's exit path.
func maybeAudit(event string, report *runtime.CheckReport, start time.Time) {
	if report == nil {
		return
	}
	// configEnabled=false: only RECONC_AUDIT env can enable today.
	// Once .reconc.yml grows an `audit.enabled` key, thread it through.
	if !audit.Enabled(report.RepoRoot, false) {
		return
	}
	entry := auditEntryFromReport(event, report, "0.1.0-dev", start)
	// Log rotation / append failures to stderr so operators notice, but
	// never fail the user's command -- audit is advisory, not blocking.
	if err := audit.Append(report.RepoRoot, entry, 0); err != nil {
		fmt.Fprintf(os.Stderr, "reconc: audit: %s\n", err)
	}
}
