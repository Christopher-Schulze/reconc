package cli

import (
	"fmt"
	"io"

	"reconc.dev/reconc/internal/atomicfile"
	"reconc.dev/reconc/internal/cireport"
	"reconc.dev/reconc/internal/runtime"
	"reconc.dev/reconc/internal/runtime/agentsession"
)

type policyReportFormat string

const (
	reportText  policyReportFormat = "text"
	reportJSON  policyReportFormat = "json"
	reportTerse policyReportFormat = "terse"
	reportSARIF policyReportFormat = "sarif"
	reportJUnit policyReportFormat = "junit"
)

func resolvePolicyReportFormat(explicit string, jsonOutput, terse, allowTerse bool) (policyReportFormat, error) {
	if jsonOutput && terse {
		return "", fmt.Errorf("--json and --terse are mutually exclusive")
	}
	if explicit != "" && (jsonOutput || terse) {
		return "", fmt.Errorf("--format cannot be combined with --json or --terse")
	}
	if explicit == "" {
		switch {
		case jsonOutput:
			return reportJSON, nil
		case terse:
			return reportTerse, nil
		default:
			return reportText, nil
		}
	}
	format := policyReportFormat(explicit)
	if format == reportTerse && !allowTerse {
		return "", fmt.Errorf("--format terse is supported only by reconc check")
	}
	if format != reportText && format != reportJSON && format != reportTerse && format != reportSARIF && format != reportJUnit {
		if allowTerse {
			return "", fmt.Errorf("--format must be text, json, terse, sarif, or junit")
		}
		return "", fmt.Errorf("--format must be text, json, sarif, or junit")
	}
	return format, nil
}

func (format policyReportFormat) ciNative() bool {
	return format == reportSARIF || format == reportJUnit
}

func (format policyReportFormat) nativeFormat() cireport.Format {
	if format == reportJUnit {
		return cireport.FormatJUnit
	}
	return cireport.FormatSARIF
}

func ciCandidate(snapshot agentsession.CompletionStateSnapshot) cireport.Candidate {
	return cireport.Candidate{
		Fingerprint: snapshot.Fingerprint, PolicyLockHash: snapshot.PolicyLockHash,
		WorktreeHash: snapshot.WorktreeHash, GitAvailable: snapshot.GitAvailable,
		WorktreeTrusted: snapshot.WorktreeTrusted, DirtyPathCount: len(snapshot.DirtyPaths),
	}
}

func ciGit(metadata runtime.GitDiffMetadata) *cireport.Git {
	return &cireport.Git{
		Mode: string(metadata.Mode), Base: metadata.Base, Head: metadata.Head,
		WritePathCount: metadata.WritePathCount,
	}
}

func writeCINativeDecision(command, version string, format policyReportFormat, outputPath string, output io.Writer, candidate agentsession.CompletionStateSnapshot, git *cireport.Git, report *runtime.CheckReport) error {
	model := cireport.FromCheck(command, version, ciCandidate(candidate), git, report)
	return writeCINativeModel(command, format, outputPath, output, model)
}

func writeCINativeFailure(command, version, repo string, format policyReportFormat, outputPath string, output io.Writer, candidate agentsession.CompletionStateSnapshot, cause error) error {
	return writeCINativeFailureCode(command, version, repo, format, outputPath, output, candidate, 1, cause)
}

func writeCINativeFailureCode(command, version, repo string, format policyReportFormat, outputPath string, output io.Writer, candidate agentsession.CompletionStateSnapshot, exitCode int, cause error) error {
	model := cireport.Operational(command, version, repo, ciCandidate(candidate), exitCode, cause)
	if !format.ciNative() {
		return &CLIError{ExitCode: exitCode, Message: "reconc " + command + ": " + model.OperationalError}
	}
	if err := writeCINativeModel(command, format, outputPath, output, model); err != nil {
		return err
	}
	return &CLIError{ExitCode: exitCode, Message: ""}
}

func writeCINativeModel(command string, format policyReportFormat, outputPath string, output io.Writer, model cireport.Model) error {
	body, err := cireport.Render(format.nativeFormat(), model)
	if err != nil {
		return &CLIError{ExitCode: 1, Message: "reconc " + command + ": render " + string(format) + ": " + err.Error()}
	}
	if outputPath != "" {
		if _, err := atomicfile.WriteIfChanged(outputPath, body, 0o644); err != nil {
			return &CLIError{ExitCode: 1, Message: "reconc " + command + ": write output: " + err.Error()}
		}
	}
	if _, err := output.Write(body); err != nil {
		return &CLIError{ExitCode: 1, Message: "reconc " + command + ": write stdout: " + err.Error()}
	}
	return nil
}
