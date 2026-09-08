package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"reconc.dev/reconc/internal/boundedio"
	"reconc.dev/reconc/internal/cievidence"
)

type ciEvidenceOptions struct {
	evidencePath    string
	requirementPath string
	candidate       cievidence.Candidate
	jsonOutput      bool
}

type ciEvidenceReport struct {
	Schema        string               `json:"schema"`
	FormatVersion string               `json:"format_version"`
	Status        string               `json:"status"`
	Candidate     cievidence.Candidate `json:"candidate"`
	Identity      string               `json:"evidence_identity,omitempty"`
	Detail        string               `json:"detail"`
}

func runCIVerifyEvidence(args []string, stdout io.Writer) error {
	options, err := parseCIEvidenceOptions(args)
	if err != nil {
		return &CLIError{ExitCode: 1, Message: "reconc ci verify-evidence: " + err.Error()}
	}
	report := ciEvidenceReport{
		Schema: "reconc.ci-verification/v1", FormatVersion: "1", Status: "blocked",
		Candidate: options.candidate,
	}
	verified, verifyErr := verifyCIEvidenceFiles(options)
	if verifyErr != nil {
		report.Detail = verifyErr.Error()
	} else {
		report.Status, report.Identity = "verified", verified.Identity
		report.Detail = "Signed CI snapshot matches the exact candidate and required checks; the caller must gate its action on this result."
	}
	if err := writeCIEvidenceReport(stdout, options.jsonOutput, report); err != nil {
		return &CLIError{ExitCode: 1, Message: "reconc ci verify-evidence: write report: " + err.Error()}
	}
	if verifyErr != nil {
		return &CLIError{ExitCode: 2, Message: ""}
	}
	return nil
}

func parseCIEvidenceOptions(args []string) (ciEvidenceOptions, error) {
	var options ciEvidenceOptions
	seen := make(map[string]bool)
	for index := 0; index < len(args); index++ {
		argument := args[index]
		if seen[argument] {
			return options, fmt.Errorf("option %q may be provided only once", argument)
		}
		seen[argument] = true
		switch argument {
		case "--json":
			options.jsonOutput = true
			continue
		case "--evidence", "--requirement", "--candidate", "--candidate-kind":
		default:
			return options, fmt.Errorf("unknown flag %q", argument)
		}
		if index+1 >= len(args) || args[index+1] == "" || strings.HasPrefix(args[index+1], "-") {
			return options, fmt.Errorf("%s requires a value", argument)
		}
		index++
		if err := assignCIEvidenceOption(&options, argument, args[index]); err != nil {
			return options, err
		}
	}
	if options.evidencePath == "" || options.requirementPath == "" || options.candidate.ObjectID == "" || options.candidate.Kind == "" {
		return options, fmt.Errorf("--evidence, --requirement, --candidate and --candidate-kind are required")
	}
	return options, nil
}

func assignCIEvidenceOption(options *ciEvidenceOptions, name, value string) error {
	switch name {
	case "--evidence":
		options.evidencePath = value
	case "--requirement":
		options.requirementPath = value
	case "--candidate":
		options.candidate.ObjectID = value
	case "--candidate-kind":
		options.candidate.Kind = cievidence.CandidateKind(value)
	default:
		return fmt.Errorf("unknown flag %q", name)
	}
	return nil
}

func verifyCIEvidenceFiles(options ciEvidenceOptions) (cievidence.Verification, error) {
	configuration, err := boundedio.ReadRegularFile(options.requirementPath, cievidence.MaxBytes)
	if err != nil {
		return cievidence.Verification{}, fmt.Errorf("read trusted CI requirement: %w", err)
	}
	requirement, err := cievidence.DecodeRequirement(configuration)
	if err != nil {
		return cievidence.Verification{}, err
	}
	evidence, err := boundedio.ReadRegularFile(options.evidencePath, cievidence.MaxBytes)
	if err != nil {
		return cievidence.Verification{}, fmt.Errorf("read signed CI evidence: %w", err)
	}
	return cievidence.Verify(evidence, requirement, options.candidate, time.Now())
}

func writeCIEvidenceReport(output io.Writer, jsonOutput bool, report ciEvidenceReport) error {
	if jsonOutput {
		return json.NewEncoder(output).Encode(report)
	}
	_, err := fmt.Fprintf(output, "CI evidence: %s\n%s\n", report.Status, report.Detail)
	return err
}
