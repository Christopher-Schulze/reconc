package cli

import (
	"encoding/json"
	"fmt"
	"io"

	"reconc.dev/reconc/internal/runtime/agentsession"
)

func runHookClaim(args []string, stdout, stderr io.Writer) (resultErr error) {
	repo := ""
	claim := ""
	sessionID := ""
	jsonOut := false
	outputPath := ""
	positional := 0
	i := 0
	for i < len(args) {
		a := args[i]
		switch a {
		case "-h", "--help":
			fmt.Fprintln(stdout, "Usage: reconc hook claim <repo> <claim-name> [--session ID] [--json] [--output PATH]")
			fmt.Fprintln(stdout, "")
			fmt.Fprintln(stdout, "Records a claim (e.g. 'ci-green') in the active session state so")
			fmt.Fprintln(stdout, "subsequent require_claim rules see it.")
			return nil
		case "--json":
			jsonOut = true
		case "--output":
			val, ok := nextArgValue(args, &i, a, argValueNoLeadingDash)
			if !ok {
				return &CLIError{ExitCode: 1, Message: "reconc hook claim: --output requires a path"}
			}
			outputPath = val
		case "--session":
			if i+1 >= len(args) {
				return &CLIError{ExitCode: 1, Message: "reconc hook claim: --session requires a value"}
			}
			sessionID = args[i+1]
			i++
		default:
			if len(a) > 0 && a[0] == '-' {
				return &CLIError{ExitCode: 1, Message: fmt.Sprintf("reconc hook claim: unknown flag %q", a)}
			}
			switch positional {
			case 0:
				repo = a
			case 1:
				claim = a
			default:
				return &CLIError{ExitCode: 1, Message: "reconc hook claim: too many positional arguments (expected <repo> <claim-name>)"}
			}
			positional++
		}
		i++
	}
	if repo == "" || claim == "" {
		return &CLIError{ExitCode: 1, Message: "reconc hook claim: usage: reconc hook claim <repo> <claim-name> [--session ID] [--json]"}
	}

	report, err := agentsession.RecordClaim(repo, claim, sessionID)
	if err != nil {
		return &CLIError{ExitCode: 1, Message: "reconc hook claim: " + err.Error()}
	}
	out, closeOutput, err := teeToFile(stdout, outputPath)
	if err != nil {
		return &CLIError{ExitCode: 1, Message: "reconc hook claim: open output file: " + err.Error()}
	}
	defer joinOutputCloseError(&resultErr, closeOutput)

	if jsonOut {
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		return enc.Encode(report)
	}
	fmt.Fprintln(out, agentsession.DescribeClaimReport(report))
	return nil
}
