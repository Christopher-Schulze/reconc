package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	reconbootstrap "reconc.dev/reconc/internal/bootstrap"
	"reconc.dev/reconc/internal/usercli"
)

func runInstallCLI(args []string, version string, stdout io.Writer) error {
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
	options, err := installCLIOptions(version)
	if err != nil {
		return &CLIError{ExitCode: 1, Message: "reconc install-cli: " + err.Error()}
	}
	report, err := usercli.InstallCurrentWithReceipt(installDir, options)
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

func ensureCurrentUserCLI(command string, version string) error {
	diagnostic, err := usercli.DiagnoseGlobal(version)
	if err != nil {
		return userCLICommandError(command, "diagnose user CLI: "+err.Error())
	}
	if diagnostic.Status == usercli.DiagnosticHealthy {
		return nil
	}
	report, err := usercli.InstallCurrentWithReceipt("", usercli.InstallOptions{Version: version})
	if err != nil {
		return userCLICommandError(command, "install user CLI: "+err.Error())
	}
	if report.Status.Ready {
		return nil
	}
	return userCLICommandError(command, "the running Reconc build was installed but is not directly callable from PATH; "+report.Status.NextAction)
}

func userCLICommandError(command, message string) error {
	if command == "init" {
		return initCLIError(message)
	}
	return bootstrapCLIError(command, message)
}

func installCLIOptions(version string) (usercli.InstallOptions, error) {
	options := usercli.InstallOptions{Version: strings.TrimSpace(version)}
	manager := usercli.Manager(strings.TrimSpace(os.Getenv("RECONC_INSTALL_MANAGER")))
	if manager == "" || manager == usercli.ManagerSource {
		options.Manager = usercli.ManagerSource
		return options, nil
	}
	if manager != usercli.ManagerDirect {
		return usercli.InstallOptions{}, fmt.Errorf("RECONC_INSTALL_MANAGER may be only direct or source")
	}
	options.Manager = manager
	options.Channel = usercli.Channel(strings.TrimSpace(os.Getenv("RECONC_INSTALL_CHANNEL")))
	if options.Channel == "" {
		options.Channel = usercli.ChannelExact
	}
	switch options.Channel {
	case usercli.ChannelStable, usercli.ChannelPreview, usercli.ChannelExact:
	default:
		return usercli.InstallOptions{}, fmt.Errorf("invalid direct installation channel %q", options.Channel)
	}
	options.ArtifactName = strings.TrimSpace(os.Getenv("RECONC_INSTALL_ARTIFACT"))
	options.ReleaseTag = strings.TrimSpace(os.Getenv("RECONC_INSTALL_RELEASE_TAG"))
	options.ProvenanceState = usercli.ProvenanceState(strings.TrimSpace(os.Getenv("RECONC_INSTALL_PROVENANCE")))
	if options.ProvenanceState == "" {
		options.ProvenanceState = usercli.ProvenanceEmbeddedVerified
	}
	switch options.ProvenanceState {
	case usercli.ProvenanceGitHubVerified, usercli.ProvenanceEmbeddedVerified:
	default:
		return usercli.InstallOptions{}, fmt.Errorf("invalid direct installation provenance %q", options.ProvenanceState)
	}
	return options, nil
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
