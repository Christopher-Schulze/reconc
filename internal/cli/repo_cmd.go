package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"

	reconbootstrap "reconc.dev/reconc/internal/bootstrap"
)

func runRepo(args []string, version string, stdout io.Writer) error {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" {
		printRepoHelp(stdout)
		return nil
	}
	if args[0] != "sync" {
		return repoCLIError(fmt.Sprintf("unknown subcommand %q", args[0]))
	}
	if len(args) == 1 {
		return repoCLIError("missing sync subcommand: plan, apply, or verify")
	}
	switch args[1] {
	case "plan":
		return runRepoSyncPlan(args[2:], version, stdout)
	case "apply":
		return runRepoSyncApply(args[2:], version, stdout)
	case "verify":
		return runRepoSyncVerify(args[2:], version, stdout)
	case "-h", "--help":
		printRepoHelp(stdout)
		return nil
	default:
		return repoCLIError(fmt.Sprintf("unknown sync subcommand %q", args[1]))
	}
}

func printRepoHelp(stdout io.Writer) {
	fmt.Fprintln(stdout, "Usage: reconc repo sync plan [repo] [--output PATH [--replace-output]] [--json]")
	fmt.Fprintln(stdout, "       reconc repo sync apply --plan PATH --digest SHA256 [--json]")
	fmt.Fprintln(stdout, "       reconc repo sync verify [repo] [--json]")
	fmt.Fprintln(stdout, "")
	fmt.Fprintln(stdout, "Planning is read-only unless --output is supplied. Apply requires the exact")
	fmt.Fprintln(stdout, "saved plan digest and mutates only receipt-owned bytes.")
}

func runRepoSyncPlan(args []string, version string, stdout io.Writer) error {
	repo := "."
	repoSet := false
	output := ""
	replaceOutput := false
	jsonOut := false
	for index := 0; index < len(args); index++ {
		switch args[index] {
		case "--output":
			if output != "" {
				return repoCLIError("--output may be specified only once")
			}
			value, ok := nextArgValue(args, &index, "--output")
			if !ok {
				return repoCLIError("--output requires a path")
			}
			output = value
		case "--replace-output":
			replaceOutput = true
		case "--json":
			jsonOut = true
		case "-h", "--help":
			printRepoHelp(stdout)
			return nil
		default:
			if strings.HasPrefix(args[index], "-") {
				return repoCLIError(fmt.Sprintf("unknown flag %q", args[index]))
			}
			if repoSet {
				return repoCLIError(fmt.Sprintf("unexpected argument %q", args[index]))
			}
			repo = args[index]
			repoSet = true
		}
	}
	if replaceOutput && output == "" {
		return repoCLIError("--replace-output requires --output PATH")
	}
	plan, err := reconbootstrap.BuildSyncPlan(repo, version)
	if err != nil {
		return repoCLIError(err.Error())
	}
	writeState := ""
	if output != "" {
		if replaceOutput {
			writeState, err = reconbootstrap.ReplaceSyncPlan(output, plan)
		} else {
			writeState, err = reconbootstrap.WriteSyncPlan(output, plan)
		}
		if err != nil {
			return repoCLIError(err.Error())
		}
	}
	if jsonOut {
		if err := writeRepoJSON(stdout, plan); err != nil {
			return err
		}
	} else {
		renderRepoSyncPlan(stdout, plan, output, writeState)
	}
	if len(plan.BlockingIssues) > 0 {
		return &CLIError{ExitCode: 1, Message: ""}
	}
	return nil
}

func runRepoSyncApply(args []string, version string, stdout io.Writer) error {
	planPath := ""
	digest := ""
	jsonOut := false
	for index := 0; index < len(args); index++ {
		switch args[index] {
		case "--plan":
			if planPath != "" {
				return repoCLIError("--plan may be specified only once")
			}
			value, ok := nextArgValue(args, &index, "--plan")
			if !ok {
				return repoCLIError("--plan requires a path")
			}
			planPath = value
		case "--digest":
			if digest != "" {
				return repoCLIError("--digest may be specified only once")
			}
			value, ok := nextArgValue(args, &index, "--digest")
			if !ok {
				return repoCLIError("--digest requires a SHA-256 value")
			}
			digest = value
		case "--json":
			jsonOut = true
		case "-h", "--help":
			printRepoHelp(stdout)
			return nil
		default:
			return repoCLIError(fmt.Sprintf("unknown argument %q", args[index]))
		}
	}
	if planPath == "" || digest == "" {
		return repoCLIError("--plan PATH and --digest SHA256 are required")
	}
	plan, err := reconbootstrap.LoadSyncPlan(planPath)
	if err != nil {
		return repoCLIError(err.Error())
	}
	if err := ensureCurrentUserCLI("repo sync apply", version); err != nil {
		return err
	}
	report, applyErr := reconbootstrap.ApplySyncPlan(plan, digest, version)
	if jsonOut {
		if err := writeRepoJSON(stdout, report); err != nil {
			return err
		}
	} else {
		renderRepoSyncReport(stdout, report)
	}
	if applyErr != nil {
		return repoCLIError(applyErr.Error())
	}
	if report.Status != reconbootstrap.SyncComplete {
		return &CLIError{ExitCode: 1, Message: ""}
	}
	return nil
}

func runRepoSyncVerify(args []string, version string, stdout io.Writer) error {
	repo := "."
	repoSet := false
	jsonOut := false
	for _, arg := range args {
		switch arg {
		case "--json":
			jsonOut = true
		case "-h", "--help":
			printRepoHelp(stdout)
			return nil
		default:
			if strings.HasPrefix(arg, "-") {
				return repoCLIError(fmt.Sprintf("unknown flag %q", arg))
			}
			if repoSet {
				return repoCLIError(fmt.Sprintf("unexpected argument %q", arg))
			}
			repo = arg
			repoSet = true
		}
	}
	verification, err := reconbootstrap.VerifyRepository(repo, version)
	if err != nil {
		return repoCLIError(err.Error())
	}
	if jsonOut {
		if err := writeRepoJSON(stdout, verification); err != nil {
			return err
		}
	} else {
		fmt.Fprintf(stdout, "Repository: %s\n", verification.RepoRoot)
		fmt.Fprintf(stdout, "Receipt: %s\n", verification.ReceiptDigest)
		for _, check := range verification.Checks {
			fmt.Fprintf(stdout, "%s %s: %s\n", check.Status, check.Name, check.Detail)
		}
		fmt.Fprintf(stdout, "Next: %s\n", verification.NextAction)
	}
	if !verification.Valid {
		return &CLIError{ExitCode: 1, Message: ""}
	}
	return nil
}

func renderRepoSyncPlan(stdout io.Writer, plan *reconbootstrap.SyncPlan, output, writeState string) {
	fmt.Fprintf(stdout, "Repository: %s\n", plan.RepoRoot)
	fmt.Fprintf(stdout, "Product: %s -> %s\n", plan.CurrentProductVersion, plan.TargetProductVersion)
	fmt.Fprintf(stdout, "Receipt: %s\n", plan.CurrentReceiptDigest)
	fmt.Fprintf(stdout, "Plan digest: %s\n", plan.PlanDigest)
	if output != "" {
		fmt.Fprintf(stdout, "Plan file: %s (%s)\n", output, writeState)
	}
	for _, action := range plan.Actions {
		fmt.Fprintf(stdout, "%s %s: %s\n", action.State, action.Path, action.Reason)
	}
	for _, issue := range plan.BlockingIssues {
		fmt.Fprintf(stdout, "BLOCKED: %s\n", issue)
	}
	if output == "" {
		fmt.Fprintln(stdout, "Next: rerun with --output PATH, review the saved plan, then apply its exact digest.")
	} else if len(plan.BlockingIssues) == 0 {
		fmt.Fprintf(stdout, "Next: reconc repo sync apply --plan %s --digest %s\n", output, plan.PlanDigest)
	} else {
		fmt.Fprintln(stdout, "Next: resolve every blocking issue, then rebuild the plan.")
	}
}

func renderRepoSyncReport(stdout io.Writer, report *reconbootstrap.SyncReport) {
	fmt.Fprintf(stdout, "Repository: %s\n", report.RepoRoot)
	fmt.Fprintf(stdout, "Status: %s\n", report.Status)
	fmt.Fprintf(stdout, "Product: %s -> %s\n", report.ProductFrom, report.ProductTo)
	fmt.Fprintf(stdout, "Receipt: %s -> %s\n", report.ReceiptFrom, report.ReceiptTo)
	fmt.Fprintf(stdout, "Changed: %s\n", displayBootstrapList(report.Changed))
	fmt.Fprintf(stdout, "Unchanged: %s\n", displayBootstrapList(report.Unchanged))
	fmt.Fprintf(stdout, "Candidates: %s\n", displayBootstrapList(report.Candidates))
	fmt.Fprintf(stdout, "Rolled back: %s\n", displayBootstrapList(report.RolledBack))
	fmt.Fprintf(stdout, "Next: %s\n", report.NextAction)
}

func writeRepoJSON(stdout io.Writer, value interface{}) error {
	encoder := json.NewEncoder(stdout)
	encoder.SetIndent("", "  ")
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		return repoCLIError("encode JSON: " + err.Error())
	}
	return nil
}

func repoCLIError(message string) error {
	return &CLIError{ExitCode: 1, Message: "reconc repo: " + message}
}
