package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"time"

	"reconc.dev/reconc/internal/policy"
	"reconc.dev/reconc/internal/runtime"
	"reconc.dev/reconc/internal/runtime/agentsession"
)

// runCI implements `reconc ci [repo] (--staged | --base REF [--head REF])
// [--read PATH ...] [--command CMD ...] [--claim NAME ...] [--json]`.
//
// Derives write_paths from a git diff (staged OR base..head range),
// merges with explicit --read/--command/--claim flags, and runs check.
// Exit codes: 0 = pass/warn, 1 = error, 2 = blocking violation.
//
// The most common shapes:
//   - reconc ci --staged                  (used by git pre-commit hook)
//   - reconc ci --base main               (used by PR / CI pipelines)
//   - reconc ci --base main --head HEAD   (explicit range)
func runCI(args []string, reconcVersion string, stdout, stderr io.Writer) (resultErr error) {
	options, help, err := parseCIOptions(args, stdout)
	if err != nil || help {
		return err
	}
	format, err := resolvePolicyReportFormat(options.format, options.jsonOutput, false, false)
	if err != nil {
		return &CLIError{ExitCode: 1, Message: "reconc ci: " + err.Error()}
	}
	prepared, exitCode, err := prepareCIEvaluation(options.repo, options.staged, options.base, options.head, options.inputs)
	if err != nil {
		return writeCINativeFailureCode("ci", reconcVersion, options.repo, format, options.outputPath, stdout, prepared.candidate, exitCode, err)
	}
	candidate, inputs, gitMeta := prepared.candidate, prepared.inputs, prepared.git

	startCI := time.Now()
	report, err := runtime.CheckRepoPolicy(candidate.RepoRoot, inputs)
	if err != nil {
		return writeCINativeFailure("ci", reconcVersion, options.repo, format, options.outputPath, stdout, candidate, err)
	}
	return finishCIDecision(reconcVersion, options.repo, format, options.outputPath, options.staged, stdout, candidate, gitMeta, report, startCI)
}

func finishCIDecision(reconcVersion, repo string, format policyReportFormat, outputPath string, staged bool, stdout io.Writer, candidate agentsession.CompletionStateSnapshot, gitMeta runtime.GitDiffMetadata, report *runtime.CheckReport, start time.Time) error {
	if staged {
		annotateStagedCommandViolations(report, candidate.RepoRoot)
	}
	if err := persistPolicyDecision("ci", candidate, report); err != nil {
		return writeCINativeFailure("ci", reconcVersion, repo, format, outputPath, stdout, candidate, fmt.Errorf("persist decision proof: %w", err))
	}
	if err := maybeAudit("ci", report, reconcVersion, start); err != nil {
		return writeCINativeFailure("ci", reconcVersion, repo, format, outputPath, stdout, candidate, fmt.Errorf("append audit evidence: %w", err))
	}
	if format.ciNative() {
		if err := writeCINativeDecision("ci", reconcVersion, format, outputPath, stdout, candidate, ciGit(gitMeta), report); err != nil {
			return err
		}
		if report.Decision == runtime.DecisionBlock {
			return &CLIError{ExitCode: 2, Message: ""}
		}
		return nil
	}
	if err := writeLegacyCIReport(format, outputPath, stdout, gitMeta, report); err != nil {
		return err
	}
	if report.Decision == runtime.DecisionBlock {
		return &CLIError{ExitCode: 2, Message: ""}
	}
	return nil
}

func writeLegacyCIReport(format policyReportFormat, outputPath string, stdout io.Writer, gitMeta runtime.GitDiffMetadata, report *runtime.CheckReport) (resultErr error) {
	out, closeOutput, err := teeToFile(stdout, outputPath)
	if err != nil {
		return &CLIError{ExitCode: 1, Message: "reconc ci: open output file: " + err.Error()}
	}
	defer joinOutputCloseError(&resultErr, closeOutput)

	if format == reportJSON {
		// Embed git metadata into the JSON output for auditability.
		payload := map[string]interface{}{
			"report": report,
			"git":    gitMeta,
		}
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		if err := enc.Encode(payload); err != nil {
			return &CLIError{ExitCode: 1, Message: "reconc ci: json encode: " + err.Error()}
		}
	} else {
		fmt.Fprintf(out, "Git:       %s (%d path(s))\n", gitMeta.GitCommand, gitMeta.WritePathCount)
		renderCheckText(report, out)
	}
	return nil
}

// annotateStagedCommandViolations explains the staged evidence contract on
// require_command_success violations. The staged gate deliberately accepts
// only index-bound command proofs (reconc exec --staged) and never plain
// session command results, because a session run does not prove it executed
// against the exact staged tree. Without this note agents burn cycles
// re-running already-green commands that can never satisfy the gate.
func annotateStagedCommandViolations(report *runtime.CheckReport, repoRoot string) {
	if report == nil {
		return
	}
	for i := range report.Violations {
		violation := &report.Violations[i]
		if violation.Kind != policy.KindRequireCommandSuccess {
			continue
		}
		previousAction := violation.RecommendedAction
		example := "<command>"
		if len(violation.RequiredCommands) > 0 {
			example = violation.RequiredCommands[0]
		}
		violation.RecommendedAction += fmt.Sprintf(
			" Staged commits accept only index-bound command proofs, not session command history; record one with: reconc exec %s --staged --shell -- %q.",
			repoRoot, example)
		if i < len(report.Actions) {
			report.Actions[i] = violation.RecommendedAction
		}
		if report.NextAction == previousAction {
			report.NextAction = violation.RecommendedAction
		}
	}
}
