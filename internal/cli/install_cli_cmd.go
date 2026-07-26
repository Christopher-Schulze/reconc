package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"

	reconbootstrap "reconc.dev/reconc/internal/bootstrap"
	"reconc.dev/reconc/internal/usercli"
)

func runInstallCLI(args []string, stdout io.Writer) error {
	installDir := ""
	jsonOut := false
	for index := 0; index < len(args); index++ {
		switch args[index] {
		case "--install-dir":
			value, ok := nextArgValue(args, &index, "--install-dir")
			if !ok {
				return &CLIError{ExitCode: 1, Message: "reconc install-cli: --install-dir requires a path"}
			}
			if installDir != "" {
				return &CLIError{ExitCode: 1, Message: "reconc install-cli: --install-dir may be specified only once"}
			}
			installDir = value
		case "--json":
			jsonOut = true
		case "-h", "--help":
			fmt.Fprintln(stdout, "Usage: reconc install-cli [--install-dir PATH] [--json]")
			fmt.Fprintln(stdout, "Atomically install the running executable as the stable user CLI.")
			fmt.Fprintln(stdout, "The command fails with exact remediation when the installed CLI is not current on PATH.")
			return nil
		default:
			return &CLIError{ExitCode: 1, Message: fmt.Sprintf("reconc install-cli: unknown argument %q", args[index])}
		}
	}
	report, err := usercli.InstallCurrent(installDir)
	if err != nil {
		return &CLIError{ExitCode: 1, Message: "reconc install-cli: " + err.Error()}
	}
	if jsonOut {
		encoder := json.NewEncoder(stdout)
		encoder.SetIndent("", "  ")
		if err := encoder.Encode(report); err != nil {
			return &CLIError{ExitCode: 1, Message: "reconc install-cli: json encode: " + err.Error()}
		}
	} else {
		action := "unchanged"
		if report.Changed {
			action = "installed"
		}
		fmt.Fprintf(stdout, "User CLI: %s (%s)\n", report.Status.TargetPath, action)
		fmt.Fprintf(stdout, "PATH command: %s\n", displayUserCLIPath(report.Status))
		fmt.Fprintf(stdout, "Next: %s\n", report.Status.NextAction)
	}
	if !report.Status.Ready {
		return &CLIError{ExitCode: 1, Message: ""}
	}
	return nil
}

func ensureCurrentUserCLI(command string) error {
	report, err := usercli.InstallCurrent("")
	if err != nil {
		return bootstrapCLIError(command, "install user CLI: "+err.Error())
	}
	if report.Status.Ready {
		return nil
	}
	return bootstrapCLIError(command, "the running Reconc build was installed but is not directly callable from PATH; "+report.Status.NextAction)
}

func appendUserCLIVerification(verification *reconbootstrap.Verification) error {
	status, err := usercli.InspectCurrent("")
	if err != nil {
		return err
	}
	checkStatus := "PASS"
	if !status.Ready {
		checkStatus = "FAIL"
		verification.Valid = false
		verification.NextAction = status.NextAction
	}
	verification.Checks = append(verification.Checks, reconbootstrap.Check{
		Name: "user-cli", Status: checkStatus,
		Detail: displayUserCLIPath(status) + "; " + status.NextAction,
	})
	sort.SliceStable(verification.Checks, func(i, j int) bool {
		return verification.Checks[i].Name < verification.Checks[j].Name
	})
	return nil
}

func displayUserCLIPath(status *usercli.Status) string {
	if status == nil || strings.TrimSpace(status.ResolvedPath) == "" {
		return "not resolvable as `reconc`"
	}
	return "resolves to " + status.ResolvedPath
}
