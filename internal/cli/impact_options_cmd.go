package cli

import (
	"fmt"
	"io"

	"reconc.dev/reconc/internal/impactlab"
	"reconc.dev/reconc/internal/runtime"
)

type impactCompareOptions struct {
	repo          string
	repoSet       bool
	candidateFile string
	pack          string
	corpusPaths   []string
	caseID        string
	jsonOutput    bool
	format        string
	deltaManifest string
	outputPath    string
	inputs        runtime.ExecutionInputs
	hasEvidence   bool
}

func parseImpactCompareOptions(args []string, output io.Writer) (impactCompareOptions, bool, error) {
	options := impactCompareOptions{repo: ".", caseID: "explicit", inputs: runtime.Empty()}
	for index := 0; index < len(args); index++ {
		help, err := options.consume(args, &index, output)
		if err != nil || help {
			return options, help, err
		}
	}
	if (options.candidateFile == "") == (options.pack == "") {
		return options, false, &CLIError{ExitCode: 1, Message: "reconc impact: specify exactly one of --candidate FILE or --pack NAME"}
	}
	if len(options.corpusPaths) == 0 && !options.hasEvidence {
		return options, false, &CLIError{ExitCode: 1, Message: "reconc impact: provide --corpus/--fixture or explicit evidence flags"}
	}
	if options.jsonOutput && options.format != "" {
		return options, false, &CLIError{ExitCode: 1, Message: "reconc impact: --json and --format are mutually exclusive"}
	}
	if _, err := resolveImpactReportFormat(options.format, options.jsonOutput); err != nil {
		return options, false, &CLIError{ExitCode: 1, Message: "reconc impact: " + err.Error()}
	}
	return options, false, nil
}

func (options *impactCompareOptions) consume(args []string, index *int, output io.Writer) (bool, error) {
	flag := args[*index]
	switch flag {
	case "-h", "--help":
		writeImpactHelp(output)
		return true, nil
	case "--json":
		options.jsonOutput = true
	case "--candidate", "--pack", "--corpus", "--fixture", "--case-id", "--output", "--format", "--delta-manifest":
		return false, options.consumeValue(flag, args, index)
	case "--read", "--write", "--command", "--command-success", "--command-failure", "--claim":
		return false, consumeImpactEvidence(flag, args, index, &options.inputs, &options.hasEvidence)
	default:
		if len(flag) > 0 && flag[0] == '-' {
			return false, &CLIError{ExitCode: 1, Message: fmt.Sprintf("reconc impact: unknown flag %q", flag)}
		}
		if options.repoSet {
			return false, &CLIError{ExitCode: 1, Message: "reconc impact: expected at most one repo path"}
		}
		options.repo, options.repoSet = flag, true
	}
	return false, nil
}

func (options *impactCompareOptions) consumeValue(flag string, args []string, index *int) error {
	value, ok := nextArgValue(args, index, flag)
	if !ok || value == "" {
		return &CLIError{ExitCode: 1, Message: "reconc impact: " + flag + " requires a value"}
	}
	switch flag {
	case "--candidate":
		options.candidateFile = value
	case "--pack":
		options.pack = value
	case "--corpus", "--fixture":
		options.corpusPaths = append(options.corpusPaths, value)
	case "--case-id":
		options.caseID = value
	case "--output":
		options.outputPath = value
	case "--format":
		options.format = value
	case "--delta-manifest":
		options.deltaManifest = value
	}
	return nil
}

type impactExportOptions struct {
	repo        string
	repoSet     bool
	caseID      string
	outputPath  string
	session     bool
	complete    []impactlab.EventClass
	inputs      runtime.ExecutionInputs
	hasEvidence bool
}

func parseImpactExportOptions(args []string) (impactExportOptions, error) {
	options := impactExportOptions{repo: ".", caseID: "explicit", inputs: runtime.Empty()}
	for index := 0; index < len(args); index++ {
		flag := args[index]
		switch flag {
		case "--session":
			options.session = true
		case "--case-id", "--output", "--complete":
			if err := options.consumeValue(flag, args, &index); err != nil {
				return options, err
			}
		case "--read", "--write", "--command", "--command-success", "--command-failure", "--claim":
			if err := consumeImpactEvidence(flag, args, &index, &options.inputs, &options.hasEvidence); err != nil {
				return options, err
			}
		default:
			if len(flag) > 0 && flag[0] == '-' {
				return options, &CLIError{ExitCode: 1, Message: fmt.Sprintf("reconc impact export: unknown flag %q", flag)}
			}
			if options.repoSet {
				return options, &CLIError{ExitCode: 1, Message: "reconc impact export: expected at most one repo path"}
			}
			options.repo, options.repoSet = flag, true
		}
	}
	if !options.session && !options.hasEvidence {
		return options, &CLIError{ExitCode: 1, Message: "reconc impact export: provide --session or explicit evidence flags"}
	}
	return options, nil
}

func (options *impactExportOptions) consumeValue(flag string, args []string, index *int) error {
	value, ok := nextArgValue(args, index, flag)
	if !ok || value == "" {
		return &CLIError{ExitCode: 1, Message: "reconc impact export: " + flag + " requires a value"}
	}
	switch flag {
	case "--case-id":
		options.caseID = value
	case "--output":
		options.outputPath = value
	case "--complete":
		classes, err := parseImpactEventClasses(value)
		if err != nil {
			return &CLIError{ExitCode: 1, Message: "reconc impact export: " + err.Error()}
		}
		options.complete = append(options.complete, classes...)
	}
	return nil
}

func consumeImpactEvidence(flag string, args []string, index *int, inputs *runtime.ExecutionInputs, seen *bool) error {
	value, ok := nextArgValue(args, index, flag)
	if !ok || value == "" {
		return &CLIError{ExitCode: 1, Message: "reconc impact: " + flag + " requires a value"}
	}
	*seen = true
	switch flag {
	case "--read":
		inputs.ReadPaths = append(inputs.ReadPaths, value)
	case "--write":
		inputs.WritePaths = append(inputs.WritePaths, value)
	case "--command":
		inputs.Commands = append(inputs.Commands, value)
	case "--claim":
		inputs.Claims = append(inputs.Claims, value)
	default:
		outcome := runtime.CommandOutcomeSuccess
		if flag == "--command-failure" {
			outcome = runtime.CommandOutcomeFailure
		}
		inputs.Commands = append(inputs.Commands, value)
		inputs.CommandResults = append(inputs.CommandResults, runtime.CommandResult{
			Command: value, Outcome: outcome, EvidenceEpoch: runtime.ExplicitEvidenceEpoch,
		})
	}
	return nil
}

func parseImpactEventClasses(value string) ([]impactlab.EventClass, error) {
	if value == "all" {
		return impactlab.AllEventClasses(), nil
	}
	values := splitCommaList(value)
	out := make([]impactlab.EventClass, 0, len(values))
	for _, item := range values {
		class := impactlab.EventClass(item)
		found := false
		for _, allowed := range impactlab.AllEventClasses() {
			if class == allowed {
				found = true
				break
			}
		}
		if !found {
			return nil, fmt.Errorf("--complete class %q is unsupported", item)
		}
		out = append(out, class)
	}
	return out, nil
}

func writeImpactHelp(output io.Writer) {
	fmt.Fprintln(output, "Usage: reconc impact [repo] (--candidate FILE | --pack NAME) [--corpus FILE | --fixture FILE] [evidence flags] [--delta-manifest FILE] [--format text|json|sarif|junit|github | --json] [--output PATH]")
	fmt.Fprintln(output, "       reconc impact export [repo] (--session | evidence flags) [--complete CLASS] [--case-id ID] [--output PATH]")
	fmt.Fprintln(output, "")
	fmt.Fprintln(output, "Compile an additive candidate in memory and compare it with the fresh current policy over bounded replay evidence.")
	fmt.Fprintln(output, "Evidence: --read PATH --write PATH --command CMD --command-success CMD --command-failure CMD --claim NAME")
	fmt.Fprintln(output, "Unreviewed newly allowed or newly blocked action deltas exit 2 after rendering the report.")
	fmt.Fprintln(output, "Unmatched always means unmatched in this corpus, never a dead or safe rule.")
}

type impactReportFormat string

const (
	impactText   impactReportFormat = "text"
	impactJSON   impactReportFormat = "json"
	impactSARIF  impactReportFormat = "sarif"
	impactJUnit  impactReportFormat = "junit"
	impactGitHub impactReportFormat = "github"
)

func resolveImpactReportFormat(explicit string, jsonOutput bool) (impactReportFormat, error) {
	if jsonOutput {
		return impactJSON, nil
	}
	if explicit == "" {
		return impactText, nil
	}
	format := impactReportFormat(explicit)
	if format != impactText && format != impactJSON && format != impactSARIF &&
		format != impactJUnit && format != impactGitHub {
		return "", fmt.Errorf("--format must be text, json, sarif, junit, or github")
	}
	return format, nil
}
