package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"reconc.dev/reconc/internal/scaffold"
)

// runInit implements `reconc init [repo] [--preset NAME ...] [--force] [--json]`.
//
// Scaffolds .reconc.yml + AGENTS.md in a fresh repo. Idempotent for
// AGENTS.md (never overwrites). Refuses to overwrite .reconc.yml
// without --force.
func runInit(args []string, stdout, stderr io.Writer) (resultErr error) {
	repo := "."
	jsonOut := false
	outputPath := ""
	opts := scaffold.Options{}

	i := 0
	for i < len(args) {
		a := args[i]
		switch a {
		case "--json":
			jsonOut = true
		case "--output":
			val, ok := nextArgValue(args, &i, a)
			if !ok {
				return &CLIError{ExitCode: 1, Message: "reconc init: --output requires a path"}
			}
			outputPath = val
		case "--force":
			opts.Force = true
		case "--preset":
			val, ok := nextArgValue(args, &i, a)
			if !ok {
				return &CLIError{ExitCode: 1, Message: "reconc init: --preset requires a value"}
			}
			opts.Presets = append(opts.Presets, val)
		case "-h", "--help":
			fmt.Fprintln(stdout, "Usage: reconc init [repo] [--preset NAME ...] [--force] [--json] [--output PATH]")
			fmt.Fprintln(stdout, "")
			fmt.Fprintln(stdout, "Scaffold a minimal .reconc.yml that extends one or more bundled presets.")
			fmt.Fprintln(stdout, "Also writes a stub AGENTS.md when no entry file (CLAUDE.md / AGENTS.md /")
			fmt.Fprintln(stdout, "start.md) is present. Never overwrites AGENTS.md; refuses to overwrite an")
			fmt.Fprintln(stdout, "existing .reconc.yml without --force.")
			return nil
		default:
			if len(a) > 0 && a[0] == '-' {
				return &CLIError{ExitCode: 1, Message: fmt.Sprintf("reconc init: unknown flag %q", a)}
			}
			repo = a
		}
		i++
	}

	report, err := scaffold.Initialize(repo, opts)
	if err != nil {
		return &CLIError{ExitCode: 1, Message: "reconc init: " + err.Error()}
	}
	out, closeOutput, err := teeToFile(stdout, outputPath)
	if err != nil {
		return &CLIError{ExitCode: 1, Message: "reconc init: open output file: " + err.Error()}
	}
	defer joinOutputCloseError(&resultErr, closeOutput)

	if jsonOut {
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		if err := enc.Encode(report); err != nil {
			return &CLIError{ExitCode: 1, Message: "reconc init: json encode: " + err.Error()}
		}
		return nil
	}

	fmt.Fprintf(out, "Initialized reconc policy at %s\n", report.RepoRoot)
	fmt.Fprintf(out, "Presets: %s\n", joinList(report.Presets))
	if len(report.Created) > 0 {
		fmt.Fprintf(out, "Created: %s\n", joinList(report.Created))
	}
	if len(report.Updated) > 0 {
		fmt.Fprintf(out, "Updated: %s\n", joinList(report.Updated))
	}
	if len(report.Skipped) > 0 {
		fmt.Fprintf(out, "Skipped: %s\n", joinList(report.Skipped))
	}
	fmt.Fprintf(out, "Next:    %s\n", report.NextAction)
	return nil
}
