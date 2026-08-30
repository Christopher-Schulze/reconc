package cli

import (
	"bytes"
	"fmt"
	"io"

	"reconc.dev/reconc/internal/atomicfile"
	"reconc.dev/reconc/internal/proofbundle"
)

func runProof(args []string, version string, stdout io.Writer) error {
	if len(args) > 0 && args[0] == "verify" {
		return runProofVerify(args[1:], stdout)
	}
	return runProofExport(args, version, stdout)
}

func runProofExport(args []string, version string, stdout io.Writer) error {
	options, help, err := parseProofExportOptions(args, stdout)
	if err != nil || help {
		return err
	}
	bundle, err := proofbundle.Generate(options.repo, version)
	if err != nil {
		return &CLIError{ExitCode: 1, Message: "reconc proof: " + err.Error()}
	}
	body, err := renderProofExport(bundle, options.format)
	if err != nil {
		return &CLIError{ExitCode: 1, Message: "reconc proof: render bundle: " + err.Error()}
	}
	if err := writeProofExport(body, options.outputPath, stdout); err != nil {
		return err
	}
	if !bundle.OK {
		return &CLIError{ExitCode: 2, Message: ""}
	}
	return nil
}

type proofExportOptions struct {
	repo       string
	format     string
	outputPath string
	repoSet    bool
}

func parseProofExportOptions(args []string, stdout io.Writer) (proofExportOptions, bool, error) {
	options := proofExportOptions{repo: ".", format: "json"}
	for index := 0; index < len(args); index++ {
		switch argument := args[index]; argument {
		case "-h", "--help":
			writeProofExportHelp(stdout)
			return options, true, nil
		case "--format":
			value, ok := nextArgValue(args, &index, argument, argValueNoLeadingDash)
			if !ok {
				return options, false, &CLIError{ExitCode: 1, Message: "reconc proof: --format requires json or markdown"}
			}
			if value != "json" && value != "markdown" {
				return options, false, &CLIError{ExitCode: 1, Message: "reconc proof: --format must be json or markdown"}
			}
			options.format = value
		case "--output":
			value, ok := nextArgValue(args, &index, argument, argValueNoLeadingDash)
			if !ok || value == "" {
				return options, false, &CLIError{ExitCode: 1, Message: "reconc proof: --output requires a path"}
			}
			options.outputPath = value
		default:
			if len(argument) > 0 && argument[0] == '-' {
				return options, false, &CLIError{ExitCode: 1, Message: fmt.Sprintf("reconc proof: unknown flag %q", argument)}
			}
			if options.repoSet {
				return options, false, &CLIError{ExitCode: 1, Message: "reconc proof: expected at most one repo path"}
			}
			options.repo, options.repoSet = argument, true
		}
	}
	return options, false, nil
}

func writeProofExportHelp(output io.Writer) {
	fmt.Fprintln(output, "Usage: reconc proof [repo] [--format json|markdown] [--output PATH]")
	fmt.Fprintln(output, "       reconc proof verify FILE [--repo REPO] [--json]")
	fmt.Fprintln(output, "")
	fmt.Fprintln(output, "Export a deterministic, portable completion proof for the current candidate.")
	fmt.Fprintln(output, "The command is read-only. Exit 0 = pass, 2 = blocked, 1 = error.")
}

func renderProofExport(bundle *proofbundle.Bundle, format string) ([]byte, error) {
	if format == "json" {
		return proofbundle.MarshalJSON(bundle)
	}
	var rendered bytes.Buffer
	if err := proofbundle.RenderMarkdown(&rendered, bundle); err != nil {
		return nil, err
	}
	return rendered.Bytes(), nil
}

func writeProofExport(body []byte, outputPath string, stdout io.Writer) error {
	if outputPath != "" {
		if _, err := atomicfile.WriteIfChanged(outputPath, body, 0o644); err != nil {
			return &CLIError{ExitCode: 1, Message: "reconc proof: write output: " + err.Error()}
		}
	}
	if _, err := stdout.Write(body); err != nil {
		return &CLIError{ExitCode: 1, Message: "reconc proof: write stdout: " + err.Error()}
	}
	return nil
}
