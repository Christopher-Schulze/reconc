package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"reconc.dev/reconc/internal/schema"
	"reconc.dev/reconc/internal/usercli"
)

func runUpdate(args []string, version string, stdout io.Writer) error {
	subcommand := "apply"
	command := "reconc update"
	requestArgs := args
	if len(args) > 0 && (args[0] == "check" || args[0] == "apply") {
		subcommand = args[0]
		command += " " + subcommand
		requestArgs = args[1:]
	}
	request, jsonOut, help, err := parseUpdateRequest(subcommand, command, requestArgs)
	if err != nil {
		if hasExactArgument(requestArgs, "--json") {
			return writeLifecycleFailure(stdout, "update."+subcommand, version, err.Error())
		}
		return err
	}
	if help {
		printUpdateHelp(stdout)
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	var progress *updateProgressView
	if !jsonOut {
		progress = newUpdateProgressView(stdout)
		fmt.Fprintln(stdout, progress.style.token(command, ansiBold))
		request.Progress = progress.observe
	}
	var report *usercli.LifecycleReport
	if subcommand == "check" {
		report, err = usercli.CheckUpdate(ctx, version, request)
	} else {
		report, err = usercli.ApplyUpdate(ctx, version, request)
	}
	if progress != nil {
		progress.close()
		fmt.Fprintln(stdout)
	}
	if err != nil {
		if jsonOut {
			return writeLifecycleFailure(stdout, "update."+subcommand, version, err.Error())
		}
		return &CLIError{ExitCode: 1, Message: "reconc update: " + err.Error()}
	}
	if err := writeLifecycleReport(stdout, report, jsonOut); err != nil {
		return &CLIError{ExitCode: 1, Message: "reconc update: " + err.Error()}
	}
	if lifecycleBlocking(report) {
		return &CLIError{ExitCode: 1, Message: ""}
	}
	return nil
}

func parseUpdateRequest(subcommand string, command string, args []string) (usercli.UpdateRequest, bool, bool, error) {
	request := usercli.UpdateRequest{}
	jsonOut := false
	help := false
	for index := 0; index < len(args); index++ {
		switch args[index] {
		case "--channel":
			value, ok := nextArgValue(args, &index, "--channel", argValueNoLeadingDash)
			if !ok {
				return request, jsonOut, false, &CLIError{ExitCode: 1, Message: "reconc update: --channel requires stable or preview"}
			}
			if request.Channel != "" {
				return request, jsonOut, false, &CLIError{ExitCode: 1, Message: "reconc update: --channel may be specified only once"}
			}
			request.Channel = usercli.Channel(value)
		case "--version":
			value, ok := nextArgValue(args, &index, "--version", argValueNoLeadingDash)
			if !ok {
				return request, jsonOut, false, &CLIError{ExitCode: 1, Message: "reconc update: --version requires a value"}
			}
			if request.Version != "" {
				return request, jsonOut, false, &CLIError{ExitCode: 1, Message: "reconc update: --version may be specified only once"}
			}
			request.Version = value
		case "--from-dir":
			value, ok := nextArgValue(args, &index, "--from-dir", argValueNoLeadingDash)
			if !ok {
				return request, jsonOut, false, &CLIError{ExitCode: 1, Message: "reconc update: --from-dir requires a path"}
			}
			if request.FromDir != "" {
				return request, jsonOut, false, &CLIError{ExitCode: 1, Message: "reconc update: --from-dir may be specified only once"}
			}
			request.FromDir = value
		case "--allow-downgrade":
			if subcommand != "apply" {
				return request, jsonOut, false, &CLIError{ExitCode: 1, Message: command + `: unknown argument "--allow-downgrade"`}
			}
			request.AllowDowngrade = true
		case "--json":
			jsonOut = true
		case "-h", "--help":
			help = true
		default:
			return request, jsonOut, false, &CLIError{
				ExitCode: 1, Message: fmt.Sprintf("%s: unknown argument %q", command, args[index]),
			}
		}
	}
	if request.Channel != "" && request.Version != "" {
		return request, jsonOut, false, &CLIError{ExitCode: 1, Message: "reconc update: --channel and --version are mutually exclusive"}
	}
	return request, jsonOut, help, nil
}

func runUninstall(args []string, version string, stdout io.Writer) error {
	request := usercli.UninstallRequest{}
	jsonOut := false
	jsonRequested := hasExactArgument(args, "--json")
	for _, argument := range args {
		switch argument {
		case "--purge-state":
			request.PurgeState = true
		case "--remove-skill":
			request.RemoveSkill = true
		case "--json":
			jsonOut = true
		case "-h", "--help":
			fmt.Fprintln(stdout, "Usage: reconc uninstall [--purge-state] [--remove-skill] [--json]")
			fmt.Fprintln(stdout, "Remove the receipt-owned CLI; preserve the skill unless --remove-skill verifies and removes it.")
			return nil
		default:
			if jsonRequested {
				return writeLifecycleFailure(stdout, "uninstall", version, fmt.Sprintf("unknown argument %q", argument))
			}
			return &CLIError{ExitCode: 1, Message: fmt.Sprintf("reconc uninstall: unknown argument %q", argument)}
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	report, err := usercli.Uninstall(ctx, version, request)
	if err != nil {
		if jsonOut {
			return writeLifecycleFailure(stdout, "uninstall", version, err.Error())
		}
		return &CLIError{ExitCode: 1, Message: "reconc uninstall: " + err.Error()}
	}
	if err := writeLifecycleReport(stdout, report, jsonOut); err != nil {
		return &CLIError{ExitCode: 1, Message: "reconc uninstall: " + err.Error()}
	}
	if lifecycleBlocking(report) {
		return &CLIError{ExitCode: 1, Message: ""}
	}
	return nil
}

func writeLifecycleReport(stdout io.Writer, report *usercli.LifecycleReport, jsonOut bool) error {
	if jsonOut {
		encoder := json.NewEncoder(stdout)
		encoder.SetIndent("", "  ")
		return encoder.Encode(report)
	}
	style := newTextStyler(stdout)
	fmt.Fprintf(stdout, "%s %s\n", lifecycleHeadline(style, report.Status), lifecycleSubject(report))
	if report.Skill != nil && report.Skill.Action != nil {
		fmt.Fprintf(stdout, "Skill action: %s (cwd: %s; authorization: %s)\n",
			strings.Join(report.Skill.Action.Argv, " "), report.Skill.Action.Cwd, report.Skill.Action.Authorization)
	}
	// Passing checks stay in --json and `reconc doctor --global`; text shows
	// only what needs attention.
	for _, check := range report.Checks {
		if check.Status != "pass" {
			fmt.Fprintf(stdout, "  %s %s: %s\n", style.statusTag(strings.ToUpper(check.Status), 4), check.Name, check.Detail)
		}
	}
	fmt.Fprintf(stdout, "%s %s\n", style.token("Next:", ansiBold), report.NextAction)
	return nil
}

func lifecycleHeadline(style textStyler, status usercli.LifecycleStatus) string {
	switch status {
	case usercli.LifecycleCurrent:
		return style.token("Up to date:", ansiBold+ansiGreen)
	case usercli.LifecycleUpdated:
		return style.token("Updated:", ansiBold+ansiGreen)
	case usercli.LifecycleUninstalled:
		return style.token("Uninstalled:", ansiBold+ansiGreen)
	case usercli.LifecycleUpdateAvailable:
		return style.token("Update available:", ansiBold+ansiCyan)
	case usercli.LifecycleRefused:
		return style.token("Refused:", ansiBold+ansiRed)
	default:
		return style.token("Failed:", ansiBold+ansiRed)
	}
}

func lifecycleSubject(report *usercli.LifecycleReport) string {
	subject := "reconc " + report.CurrentVersion
	if report.TargetVersion != nil && *report.TargetVersion != report.CurrentVersion {
		subject += " → " + *report.TargetVersion
	}
	if report.Owner == nil {
		subject += " (unowned)"
	} else {
		subject += " (" + string(*report.Owner) + ")"
	}
	if report.Skill != nil {
		subject += ", skill " + report.Skill.State.Describe()
	}
	return subject
}

func lifecycleBlocking(report *usercli.LifecycleReport) bool {
	return report == nil ||
		report.Status == usercli.LifecycleRefused ||
		report.Status == usercli.LifecycleFailed
}

func writeLifecycleFailure(stdout io.Writer, operation string, version string, cause string) error {
	report := &usercli.LifecycleReport{
		Schema: schema.Resolve(schema.GlobalLifecycle), FormatVersion: usercli.LifecycleFormatVersion,
		Operation: operation, Status: usercli.LifecycleFailed, Changed: false,
		CurrentVersion: version, Checks: []usercli.DiagnosticCheck{{
			Name: "request", Status: "fail", Detail: cause,
		}},
		Actions:    []usercli.DiagnosticAction{},
		NextAction: "Correct the reported error and rerun the command.",
	}
	if err := writeLifecycleReport(stdout, report, true); err != nil {
		return &CLIError{ExitCode: 1, Message: "reconc lifecycle: " + err.Error()}
	}
	return &CLIError{ExitCode: 1, Message: ""}
}

func hasExactArgument(args []string, value string) bool {
	for _, argument := range args {
		if argument == value {
			return true
		}
	}
	return false
}

func printUpdateHelp(stdout io.Writer) {
	fmt.Fprintln(stdout, "Usage: reconc update [--channel stable|preview | --version VERSION] [--allow-downgrade] [--from-dir PATH] [--json]")
	fmt.Fprintln(stdout, "Check the binary and owned skill independently; leave both unchanged when already current, or update stale owned components together.")
	fmt.Fprintln(stdout, "A missing skill is proposed as an explicit install-cli --skill-only action, never installed by update.")
}
