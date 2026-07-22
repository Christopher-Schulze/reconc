package cli

import (
	"bytes"
	"fmt"
	"io"

	"reconc.dev/reconc/internal/atomicfile"
	"reconc.dev/reconc/internal/proofbundle"
)

func runProof(args []string, version string, stdout io.Writer) error {
	repo := "."
	format := "json"
	outputPath := ""
	for index := 0; index < len(args); index++ {
		switch argument := args[index]; argument {
		case "-h", "--help":
			fmt.Fprintln(stdout, "Usage: reconc proof [repo] [--format json|markdown] [--output PATH]")
			fmt.Fprintln(stdout, "")
			fmt.Fprintln(stdout, "Export a deterministic, portable completion proof for the current candidate.")
			fmt.Fprintln(stdout, "The command is read-only. Exit 0 = pass, 2 = blocked, 1 = error.")
			return nil
		case "--format":
			value, ok := nextArgValue(args, &index, argument)
			if !ok {
				return &CLIError{ExitCode: 1, Message: "reconc proof: --format requires json or markdown"}
			}
			if value != "json" && value != "markdown" {
				return &CLIError{ExitCode: 1, Message: "reconc proof: --format must be json or markdown"}
			}
			format = value
		case "--output":
			value, ok := nextArgValue(args, &index, argument)
			if !ok || value == "" {
				return &CLIError{ExitCode: 1, Message: "reconc proof: --output requires a path"}
			}
			outputPath = value
		default:
			if len(argument) > 0 && argument[0] == '-' {
				return &CLIError{ExitCode: 1, Message: fmt.Sprintf("reconc proof: unknown flag %q", argument)}
			}
			repo = argument
		}
	}

	bundle, err := proofbundle.Generate(repo, version)
	if err != nil {
		return &CLIError{ExitCode: 1, Message: "reconc proof: " + err.Error()}
	}
	var body []byte
	if format == "json" {
		body, err = proofbundle.MarshalJSON(bundle)
	} else {
		var rendered bytes.Buffer
		err = proofbundle.RenderMarkdown(&rendered, bundle)
		body = rendered.Bytes()
	}
	if err != nil {
		return &CLIError{ExitCode: 1, Message: "reconc proof: render bundle: " + err.Error()}
	}
	if outputPath != "" {
		if _, err := atomicfile.WriteIfChanged(outputPath, body, 0o644); err != nil {
			return &CLIError{ExitCode: 1, Message: "reconc proof: write output: " + err.Error()}
		}
	}
	if _, err := stdout.Write(body); err != nil {
		return &CLIError{ExitCode: 1, Message: "reconc proof: write stdout: " + err.Error()}
	}
	if !bundle.OK {
		return &CLIError{ExitCode: 2, Message: ""}
	}
	return nil
}
