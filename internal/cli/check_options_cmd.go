package cli

import (
	"encoding/json"
	"fmt"
	"io"

	"reconc.dev/reconc/internal/runtime"
)

type checkOptions struct {
	repo       string
	repoSet    bool
	format     string
	jsonOutput bool
	terse      bool
	outputPath string
	inputs     runtime.ExecutionInputs
}

func parseCheckOptions(args []string, output io.Writer) (checkOptions, bool, error) {
	options := checkOptions{repo: ".", inputs: runtime.Empty()}
	for index := 0; index < len(args); index++ {
		help, err := options.consume(args, &index, output)
		if err != nil || help {
			return options, help, err
		}
	}
	return options, false, nil
}

func (options *checkOptions) consume(args []string, index *int, output io.Writer) (bool, error) {
	argument := args[*index]
	switch argument {
	case "-h", "--help":
		writeCheckHelp(output)
		return true, nil
	case "--json":
		options.jsonOutput = true
	case "--terse":
		options.terse = true
	case "--auto-claim":
		if detectCIEnvironment() {
			options.inputs.Claims = append(options.inputs.Claims, "ci-green")
		}
	case "--format", "--output":
		return false, options.consumeOutputFlag(argument, args, index)
	case "--read", "--write", "--command", "--command-success", "--command-failure", "--claim":
		return false, options.consumeEvidenceFlag(argument, args, index)
	default:
		return false, options.consumeRepo(argument)
	}
	return false, nil
}

func (options *checkOptions) consumeOutputFlag(flag string, args []string, index *int) error {
	value, ok := nextArgValue(args, index, flag)
	if !ok || value == "" {
		return &CLIError{ExitCode: 1, Message: "reconc check: " + flag + " requires a value"}
	}
	if flag == "--format" {
		options.format = value
	} else {
		options.outputPath = value
	}
	return nil
}

func (options *checkOptions) consumeEvidenceFlag(flag string, args []string, index *int) error {
	value, ok := nextArgValue(args, index, flag)
	if !ok {
		return &CLIError{ExitCode: 1, Message: "reconc check: " + flag + " requires a value"}
	}
	switch flag {
	case "--read":
		options.inputs.ReadPaths = append(options.inputs.ReadPaths, value)
	case "--write":
		options.inputs.WritePaths = append(options.inputs.WritePaths, value)
	case "--command":
		options.inputs.Commands = append(options.inputs.Commands, value)
	case "--command-success", "--command-failure":
		options.addCommandResult(flag, value)
	case "--claim":
		options.inputs.Claims = append(options.inputs.Claims, value)
	}
	return nil
}

func (options *checkOptions) addCommandResult(flag, command string) {
	outcome := runtime.CommandOutcomeSuccess
	if flag == "--command-failure" {
		outcome = runtime.CommandOutcomeFailure
	}
	options.inputs.Commands = append(options.inputs.Commands, command)
	options.inputs.CommandResults = append(options.inputs.CommandResults, runtime.CommandResult{
		Command: command, Outcome: outcome,
	})
}

func (options *checkOptions) consumeRepo(argument string) error {
	if len(argument) > 0 && argument[0] == '-' {
		return &CLIError{ExitCode: 1, Message: fmt.Sprintf("reconc check: unknown flag %q", argument)}
	}
	if options.repoSet {
		return &CLIError{ExitCode: 1, Message: "reconc check: expected at most one repo path"}
	}
	options.repo, options.repoSet = argument, true
	return nil
}

func writeCheckHelp(output io.Writer) {
	fmt.Fprintln(output, "Usage: reconc check [repo] [--read PATH] [--write PATH]")
	fmt.Fprintln(output, "                    [--command CMD] [--command-success CMD] [--command-failure CMD]")
	fmt.Fprintln(output, "                    [--claim NAME] [--auto-claim] [--json | --terse]")
	fmt.Fprintln(output, "                    [--format text|json|terse|sarif|junit] [--output PATH]")
	fmt.Fprintln(output, "")
	fmt.Fprintln(output, "Evaluate runtime evidence against the compiled policy lockfile.")
	fmt.Fprintln(output, "  --json / --terse  compatibility aliases for full or minimal JSON")
	fmt.Fprintln(output, "  --format sarif     deterministic SARIF 2.1.0 for code scanning")
	fmt.Fprintln(output, "  --format junit     deterministic JUnit XML for test-report consumers")
	fmt.Fprintln(output, "  --auto-claim       assert ci-green when a known CI environment is present")
	fmt.Fprintln(output, "  --output PATH      atomically write SARIF/JUnit; other formats retain existing output behavior")
	fmt.Fprintln(output, "Exit codes: 0 = pass/warn, 1 = error, 2 = blocking violation.")
}

func writeLegacyCheckReport(format policyReportFormat, outputPath string, output io.Writer, report *runtime.CheckReport) (resultErr error) {
	out, closeOutput, err := teeToFile(output, outputPath)
	if err != nil {
		return &CLIError{ExitCode: 1, Message: "reconc check: open output file: " + err.Error()}
	}
	defer joinOutputCloseError(&resultErr, closeOutput)
	switch format {
	case reportTerse:
		return encodeCheckJSON(out, report.Terse(), false, "terse")
	case reportJSON:
		return encodeCheckJSON(out, report, true, "json")
	default:
		renderCheckText(report, out)
		return nil
	}
}

func encodeCheckJSON(output io.Writer, value interface{}, indent bool, name string) error {
	encoder := json.NewEncoder(output)
	if indent {
		encoder.SetIndent("", "  ")
	}
	if err := encoder.Encode(value); err != nil {
		return &CLIError{ExitCode: 1, Message: "reconc check: " + name + " encode: " + err.Error()}
	}
	return nil
}
