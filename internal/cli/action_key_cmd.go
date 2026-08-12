package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"reconc.dev/reconc/internal/actionstate"
)

const actionKeyReportFormat = "1"

type actionKeyInitReport struct {
	FormatVersion string `json:"format_version"`
	Status        string `json:"status"`
	KeyID         string `json:"key_id"`
	ReconcHome    string `json:"reconc_home"`
}

func runActionKey(args []string, stdout io.Writer) error {
	if len(args) == 0 {
		return actionLogCLIError("action key", "missing subcommand (init)")
	}
	if len(args) == 1 && isHelpFlag(args[0]) {
		fmt.Fprintln(stdout, "Usage: reconc action key init [--reconc-home PATH] [--json]")
		fmt.Fprintln(stdout, "Initialize the private operator-owned action identity key exactly once.")
		return nil
	}
	if args[0] != "init" {
		return actionLogCLIError("action key", fmt.Sprintf("unknown subcommand %q (expected init)", args[0]))
	}
	return runActionKeyInit(args[1:], stdout)
}

func runActionKeyInit(args []string, stdout io.Writer) error {
	home := ""
	jsonOutput := false
	seen := make(map[string]bool)
	for index := 0; index < len(args); index++ {
		argument := args[index]
		switch argument {
		case "--json":
			if seen[argument] {
				return actionLogCLIError("action key init", "--json may be specified only once")
			}
			seen[argument], jsonOutput = true, true
		case "--reconc-home":
			value, ok := nextActionLogValue(args, &index, argument)
			if !ok || value == "" || seen[argument] {
				return actionLogCLIError("action key init", "--reconc-home requires one path")
			}
			seen[argument], home = true, value
		default:
			if strings.HasPrefix(argument, "-") {
				return actionLogCLIError("action key init", fmt.Sprintf("unknown flag %q", argument))
			}
			return actionLogCLIError("action key init", "positional arguments are not accepted")
		}
	}
	resolvedHome, err := actionstate.ResolveHome(home)
	if err != nil {
		return actionLogCLIError("action key init", err.Error())
	}
	keyID, err := actionstate.CreateIdentityKey(resolvedHome, time.Now().UTC())
	if err != nil {
		return actionLogCLIError("action key init", err.Error())
	}
	report := actionKeyInitReport{
		FormatVersion: actionKeyReportFormat,
		Status:        "initialized",
		KeyID:         keyID,
		ReconcHome:    resolvedHome,
	}
	if jsonOutput {
		if err := json.NewEncoder(stdout).Encode(report); err != nil {
			return actionLogCLIError("action key init", "write JSON output: "+err.Error())
		}
		return nil
	}
	if _, err := fmt.Fprintf(stdout, "Initialized action identity key %s in %s\n", report.KeyID, report.ReconcHome); err != nil {
		return actionLogCLIError("action key init", "write output: "+err.Error())
	}
	return nil
}
