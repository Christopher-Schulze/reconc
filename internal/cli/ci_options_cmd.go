package cli

import (
	"fmt"
	"io"

	"reconc.dev/reconc/internal/runtime"
)

type ciOptions struct {
	repo                   string
	repoSet                bool
	format                 string
	jsonOutput             bool
	staged                 bool
	outputPath             string
	base                   string
	head                   string
	explicitCommandOutcome bool
	inputs                 runtime.ExecutionInputs
}

func parseCIOptions(args []string, output io.Writer) (ciOptions, bool, error) {
	options := ciOptions{repo: ".", inputs: runtime.Empty()}
	for index := 0; index < len(args); index++ {
		help, err := options.consume(args, &index, output)
		if err != nil || help {
			return options, help, err
		}
	}
	if options.staged && options.explicitCommandOutcome {
		return options, false, &CLIError{ExitCode: 1, Message: "reconc ci: --command-success and --command-failure are not accepted with --staged; run commands through reconc exec --staged"}
	}
	return options, false, nil
}

func (options *ciOptions) consume(args []string, index *int, output io.Writer) (bool, error) {
	argument := args[*index]
	switch argument {
	case "-h", "--help":
		writeCIHelp(output)
		return true, nil
	case "--json":
		options.jsonOutput = true
	case "--staged":
		options.staged = true
	case "--auto-claim":
		if detectCIEnvironment() {
			options.inputs.Claims = append(options.inputs.Claims, "ci-green")
		}
	case "--format", "--output", "--base", "--head":
		return false, options.consumeValueFlag(argument, args, index)
	case "--read", "--command", "--command-success", "--command-failure", "--claim":
		return false, options.consumeEvidenceFlag(argument, args, index)
	default:
		return false, options.consumeRepo(argument)
	}
	return false, nil
}

func (options *ciOptions) consumeValueFlag(flag string, args []string, index *int) error {
	value, ok := nextArgValue(args, index, flag)
	if !ok || value == "" {
		return &CLIError{ExitCode: 1, Message: "reconc ci: " + flag + " requires a value"}
	}
	switch flag {
	case "--format":
		options.format = value
	case "--output":
		options.outputPath = value
	case "--base":
		options.base = value
	case "--head":
		options.head = value
	}
	return nil
}

func (options *ciOptions) consumeEvidenceFlag(flag string, args []string, index *int) error {
	value, ok := nextArgValue(args, index, flag)
	if !ok {
		return &CLIError{ExitCode: 1, Message: "reconc ci: " + flag + " requires a value"}
	}
	switch flag {
	case "--read":
		options.inputs.ReadPaths = append(options.inputs.ReadPaths, value)
	case "--command":
		options.inputs.Commands = append(options.inputs.Commands, value)
	case "--command-success", "--command-failure":
		options.addCommandResult(flag, value)
	case "--claim":
		options.inputs.Claims = append(options.inputs.Claims, value)
	}
	return nil
}

func (options *ciOptions) addCommandResult(flag, command string) {
	outcome := runtime.CommandOutcomeSuccess
	if flag == "--command-failure" {
		outcome = runtime.CommandOutcomeFailure
	}
	options.explicitCommandOutcome = true
	options.inputs.Commands = append(options.inputs.Commands, command)
	options.inputs.CommandResults = append(options.inputs.CommandResults, runtime.CommandResult{
		Command: command, Outcome: outcome, EvidenceEpoch: runtime.ExplicitEvidenceEpoch,
	})
}

func (options *ciOptions) consumeRepo(argument string) error {
	if len(argument) > 0 && argument[0] == '-' {
		return &CLIError{ExitCode: 1, Message: fmt.Sprintf("reconc ci: unknown flag %q", argument)}
	}
	if options.repoSet {
		return &CLIError{ExitCode: 1, Message: "reconc ci: expected at most one repo path"}
	}
	options.repo, options.repoSet = argument, true
	return nil
}

func writeCIHelp(output io.Writer) {
	fmt.Fprintln(output, "Usage: reconc ci [repo] (--staged | --base REF [--head REF])")
	fmt.Fprintln(output, "                 [--read PATH] [--command CMD] [--command-success CMD]")
	fmt.Fprintln(output, "                 [--command-failure CMD] [--claim NAME] [--auto-claim] [--json]")
	fmt.Fprintln(output, "                 [--format text|json|sarif|junit] [--output PATH]")
	fmt.Fprintln(output, "")
	fmt.Fprintln(output, "Derive write_paths from git diff and run policy check.")
	fmt.Fprintln(output, "  --staged                 git diff --cached --name-only (pre-commit)")
	fmt.Fprintln(output, "  --base REF [--head REF]  git diff base...head --name-only (PR/CI)")
	fmt.Fprintln(output, "  --staged rejects explicit command outcome flags; use reconc exec --staged")
	fmt.Fprintln(output, "  --format sarif|junit emits a bounded, deterministic CI-native report")
	fmt.Fprintln(output, "Exit codes: 0 = pass/warn, 1 = error, 2 = blocking violation.")
}
