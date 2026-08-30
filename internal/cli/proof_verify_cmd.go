package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"reconc.dev/reconc/internal/proofbundle"
)

const proofVerificationFormatVersion = "reconc-proof-verification/v1"

const proofVerificationTrust = "The unsigned self-digest proves bundle integrity, not author identity or trusted publication provenance."

type proofVerificationReport struct {
	FormatVersion        string   `json:"format_version"`
	Status               string   `json:"status"`
	IntegrityValid       bool     `json:"integrity_valid"`
	Decision             string   `json:"decision,omitempty"`
	BundleDigest         string   `json:"bundle_digest,omitempty"`
	CandidateFingerprint string   `json:"candidate_fingerprint,omitempty"`
	LocalCandidateMatch  *bool    `json:"local_candidate_match,omitempty"`
	Mismatches           []string `json:"mismatches"`
	Detail               string   `json:"detail"`
	Trust                string   `json:"trust"`
}

type proofVerifyOptions struct {
	filePath   string
	repo       string
	bindRepo   bool
	jsonOutput bool
}

func runProofVerify(args []string, stdout io.Writer) error {
	options, help, err := parseProofVerifyOptions(args, stdout)
	if err != nil || help {
		return err
	}
	report, exitCode := verifyProofFile(options)
	if err := writeProofVerificationReport(report, options.jsonOutput, stdout); err != nil {
		return &CLIError{ExitCode: 1, Message: "reconc proof verify: write report: " + err.Error()}
	}
	if exitCode != 0 {
		return &CLIError{ExitCode: exitCode, Message: ""}
	}
	return nil
}

func parseProofVerifyOptions(args []string, stdout io.Writer) (proofVerifyOptions, bool, error) {
	var options proofVerifyOptions
	for index := 0; index < len(args); index++ {
		switch argument := args[index]; argument {
		case "-h", "--help":
			writeProofVerifyHelp(stdout)
			return options, true, nil
		case "--repo":
			if options.bindRepo {
				return options, false, &CLIError{ExitCode: 1, Message: "reconc proof verify: --repo may be provided only once"}
			}
			value, ok := nextArgValue(args, &index, argument, argValueNoLeadingDash)
			if !ok || strings.TrimSpace(value) == "" || strings.HasPrefix(value, "-") {
				return options, false, &CLIError{ExitCode: 1, Message: "reconc proof verify: --repo requires a path"}
			}
			options.repo, options.bindRepo = value, true
		case "--json":
			options.jsonOutput = true
		default:
			if strings.HasPrefix(argument, "-") {
				return options, false, &CLIError{ExitCode: 1, Message: fmt.Sprintf("reconc proof verify: unknown flag %q", argument)}
			}
			if options.filePath != "" {
				return options, false, &CLIError{ExitCode: 1, Message: "reconc proof verify: expected exactly one bundle file"}
			}
			options.filePath = argument
		}
	}
	if options.filePath == "" {
		return options, false, &CLIError{ExitCode: 1, Message: "reconc proof verify: bundle file is required"}
	}
	return options, false, nil
}

func writeProofVerifyHelp(output io.Writer) {
	fmt.Fprintln(output, "Usage: reconc proof verify FILE [--repo REPO] [--json]")
	fmt.Fprintln(output, "")
	fmt.Fprintln(output, "Strictly verify one received proof bundle offline; --repo binds it to a fresh local candidate.")
	fmt.Fprintln(output, "Exit 0 = valid passing proof, 2 = valid block or candidate mismatch, 1 = invalid input or operational error.")
	fmt.Fprintln(output, proofVerificationTrust)
}

func verifyProofFile(options proofVerifyOptions) (proofVerificationReport, int) {
	report := proofVerificationReport{
		FormatVersion: proofVerificationFormatVersion,
		Status:        "error", Mismatches: []string{}, Trust: proofVerificationTrust,
	}
	bundle, err := proofbundle.DecodeFile(options.filePath)
	if err != nil {
		report.Status, report.Detail = proofVerificationError(err)
		return report, 1
	}
	report.IntegrityValid = true
	report.Decision = bundle.Decision
	report.BundleDigest = bundle.Digest
	report.CandidateFingerprint = bundle.Candidate.Fingerprint
	report.Status = "valid"
	report.Detail = "bundle contract and self-digest are valid"
	exitCode := 0
	if !bundle.OK {
		report.Status, report.Detail, exitCode = "blocking", "bundle integrity is valid and its completion decision is blocking", 2
	}
	if !options.bindRepo {
		return report, exitCode
	}
	binding, err := proofbundle.VerifyRepository(bundle, options.repo)
	if err != nil {
		report.Status, report.Detail = "error", "local candidate evaluation failed: "+err.Error()
		return report, 1
	}
	report.LocalCandidateMatch = boolPointer(binding.Match)
	report.Mismatches = append([]string{}, binding.Mismatches...)
	if !binding.Match {
		report.Status, report.Detail = "candidate-mismatch", "bundle integrity is valid, but the fresh local completion snapshot does not match"
		return report, 2
	}
	report.Detail += "; fresh local completion snapshot matches"
	return report, exitCode
}

func proofVerificationError(err error) (string, string) {
	switch {
	case errors.Is(err, proofbundle.ErrUnsupportedContract):
		return "unsupported", err.Error()
	case errors.Is(err, proofbundle.ErrMalformedInput):
		return "malformed", err.Error()
	case errors.Is(err, proofbundle.ErrUnsafeInput):
		return "unsafe-input", err.Error()
	case errors.Is(err, proofbundle.ErrInvalidContract):
		return "invalid", err.Error()
	default:
		return "error", err.Error()
	}
}

func writeProofVerificationReport(report proofVerificationReport, jsonOutput bool, output io.Writer) error {
	if jsonOutput {
		encoder := json.NewEncoder(output)
		encoder.SetIndent("", "  ")
		return encoder.Encode(report)
	}
	fmt.Fprintf(output, "Proof verification: %s\n", report.Status)
	fmt.Fprintf(output, "Integrity valid: %t\n", report.IntegrityValid)
	if report.Decision != "" {
		fmt.Fprintf(output, "Decision: %s\n", report.Decision)
	}
	if report.LocalCandidateMatch != nil {
		fmt.Fprintf(output, "Local candidate match: %t\n", *report.LocalCandidateMatch)
	}
	if len(report.Mismatches) > 0 {
		fmt.Fprintf(output, "Mismatches: %s\n", strings.Join(report.Mismatches, ", "))
	}
	fmt.Fprintf(output, "Detail: %s\n", report.Detail)
	fmt.Fprintf(output, "Trust: %s\n", report.Trust)
	return nil
}
