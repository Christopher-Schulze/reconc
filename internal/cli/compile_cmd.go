package cli

import (
	"encoding/json"
	"fmt"
	"io"

	"reconc.dev/reconc/internal/compiler"
	"reconc.dev/reconc/internal/ingest"
)

// runCompile implements `reconc compile [repo] [--json]`.
//
// Loads sources, parses rules, computes the digest, and writes
// .reconc/policy.lock.json. Returns a CLIError with exit 1 on any
// pipeline failure (PolicySourceError, RuleValidationError, etc.).
func runCompile(args []string, version string, stdout, stderr io.Writer) error {
	return runCompileCommand("compile", args, version, stdout, stderr)
}

// runRefresh implements the explicit policy-refresh command. It intentionally
// shares the compiler pipeline with `compile`; the distinct name makes every
// read-only command's remediation precise without hiding repository writes.
func runRefresh(args []string, version string, stdout, stderr io.Writer) error {
	return runCompileCommand("refresh", args, version, stdout, stderr)
}

func runCompileCommand(command string, args []string, version string, stdout, stderr io.Writer) (resultErr error) {
	repo := "."
	repoSet := false
	jsonOut := false
	strictConflicts := false
	outputPath := ""
	i := 0
	for i < len(args) {
		a := args[i]
		switch a {
		case "--json":
			jsonOut = true
		case "--strict-conflicts":
			strictConflicts = true
		case "--output":
			val, ok := nextArgValue(args, &i, a, argValueNoLeadingDash)
			if !ok {
				return &CLIError{ExitCode: 1, Message: "reconc " + command + ": --output requires a path"}
			}
			outputPath = val
		case "-h", "--help":
			fmt.Fprintf(stdout, "Usage: reconc %s [repo] [--json] [--strict-conflicts] [--output PATH]\n", command)
			fmt.Fprintf(stdout, "Explicitly compile policy sources into %s.\n", ingest.LockfilePath)
			fmt.Fprintln(stdout, "--strict-conflicts: exit 1 if any rule conflicts are detected.")
			fmt.Fprintln(stdout, "--output PATH: write the primary output to stdout and PATH.")
			return nil
		default:
			if len(a) > 0 && a[0] == '-' {
				return &CLIError{ExitCode: 1, Message: fmt.Sprintf("reconc %s: unknown flag %q", command, a)}
			}
			if repoSet {
				return &CLIError{ExitCode: 1, Message: "reconc " + command + ": expected at most one repo path"}
			}
			repo = a
			repoSet = true
		}
		i++
	}

	compiled, err := compiler.CompileRepoPolicy(repo, version)
	if err != nil {
		return &CLIError{ExitCode: 1, Message: "reconc " + command + ": " + err.Error()}
	}
	out, closeOutput, err := teeToFile(stdout, outputPath)
	if err != nil {
		return &CLIError{ExitCode: 1, Message: "reconc " + command + ": open output file: " + err.Error()}
	}
	defer joinOutputCloseError(&resultErr, closeOutput)

	if jsonOut {
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		if err := enc.Encode(compiled); err != nil {
			return &CLIError{ExitCode: 1, Message: "reconc " + command + ": json encode: " + err.Error()}
		}
		if strictConflicts && len(compiled.Conflicts) > 0 {
			return commitOutput(closeOutput, &CLIError{ExitCode: 1, Message: fmt.Sprintf("reconc %s: %d rule conflict(s) detected under --strict-conflicts", command, len(compiled.Conflicts))})
		}
		return nil
	}

	fmt.Fprintf(out, "Compiled %d rules from %d sources into %s for %s\n",
		compiled.RuleCount, compiled.SourceCount, compiled.LockfilePath, compiled.RepoRoot)
	fmt.Fprintf(out, "Default mode:  %s\n", compiled.DefaultMode)
	fmt.Fprintf(out, "Source digest: %s\n", compiled.SourceDigest)
	if len(compiled.Warnings) > 0 {
		fmt.Fprintf(out, "Warnings (%d):\n", len(compiled.Warnings))
		for _, w := range compiled.Warnings {
			fmt.Fprintf(out, "  - %s\n", w)
		}
	}
	if len(compiled.Conflicts) > 0 {
		fmt.Fprintf(out, "Conflicts (%d):\n", len(compiled.Conflicts))
		for _, cf := range compiled.Conflicts {
			fmt.Fprintf(out, "  - [%s] %s\n", cf.Kind, cf.Description)
		}
	}
	if strictConflicts && len(compiled.Conflicts) > 0 {
		return commitOutput(closeOutput, &CLIError{ExitCode: 1, Message: fmt.Sprintf("reconc %s: %d rule conflict(s) detected under --strict-conflicts", command, len(compiled.Conflicts))})
	}
	return nil
}
