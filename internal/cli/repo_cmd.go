package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
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
		return repoCLIError("missing sync subcommand: plan, apply, resolve, verify, or recover")
	}
	switch args[1] {
	case "plan":
		return runRepoSyncPlan(args[2:], version, stdout)
	case "apply":
		return runRepoSyncApply(args[2:], version, stdout)
	case "resolve":
		return runRepoSyncResolve(args[2:], version, stdout)
	case "verify":
		return runRepoSyncVerify(args[2:], version, stdout)
	case "recover":
		return runRepoSyncRecover(args[2:], stdout)
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
	fmt.Fprintln(stdout, "       reconc repo sync resolve --plan PATH --digest SHA256 --path RELATIVE --strategy keep-current|use-target|use-binary [binary flags] [--json]")
	fmt.Fprintln(stdout, "         binary flags: --binary PATH --checksum SHA256 --platform OS/ARCH")
	fmt.Fprintln(stdout, "       reconc repo sync verify [repo] [--json]")
	fmt.Fprintln(stdout, "       reconc repo sync recover [repo] [--json]")
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

func runRepoSyncResolve(args []string, version string, stdout io.Writer) error {
	planPath := ""
	digest := ""
	targetPath := ""
	strategy := ""
	binaryPath := ""
	checksum := ""
	platform := ""
	jsonOut := false
	for index := 0; index < len(args); index++ {
		flag := args[index]
		switch flag {
		case "--plan", "--digest", "--path", "--strategy", "--binary", "--checksum", "--platform":
			value, ok := nextArgValue(args, &index, flag)
			if !ok {
				return repoCLIError(flag + " requires a value")
			}
			target := &planPath
			switch flag {
			case "--digest":
				target = &digest
			case "--path":
				target = &targetPath
			case "--strategy":
				target = &strategy
			case "--binary":
				target = &binaryPath
			case "--checksum":
				target = &checksum
			case "--platform":
				target = &platform
			}
			if *target != "" {
				return repoCLIError(flag + " may be specified only once")
			}
			*target = value
		case "--json":
			jsonOut = true
		case "-h", "--help":
			printRepoHelp(stdout)
			return nil
		default:
			return repoCLIError(fmt.Sprintf("unknown argument %q", flag))
		}
	}
	if planPath == "" || digest == "" || targetPath == "" || strategy == "" {
		return repoCLIError("--plan, --digest, --path, and --strategy are required")
	}
	resolutionStrategy := reconbootstrap.SyncResolutionStrategy(strategy)
	switch resolutionStrategy {
	case reconbootstrap.SyncKeepCurrent, reconbootstrap.SyncUseTarget, reconbootstrap.SyncUseBinary:
	default:
		return repoCLIError("--strategy must be keep-current, use-target, or use-binary")
	}
	var binary *reconbootstrap.BinarySelection
	binaryInputs := binaryPath != "" || checksum != "" || platform != ""
	if binaryInputs {
		targetOS, targetArch, ok := strings.Cut(platform, "/")
		if binaryPath == "" || checksum == "" || !ok || targetOS == "" || targetArch == "" {
			return repoCLIError("--binary PATH, --checksum SHA256, and --platform OS/ARCH must be supplied together")
		}
		var err error
		binary, err = reconbootstrap.BinarySelectionFor(binaryPath, checksum, targetOS, targetArch)
		if err != nil {
			return repoCLIError(err.Error())
		}
	}
	plan, err := reconbootstrap.LoadSyncPlan(planPath)
	if err != nil {
		return repoCLIError(err.Error())
	}
	if err := ensureCurrentUserCLI("repo sync resolve", version); err != nil {
		return err
	}
	report, resolveErr := reconbootstrap.ResolveRepositorySync(reconbootstrap.SyncResolutionRequest{
		Plan: plan, ExactDigest: digest, Path: targetPath,
		Strategy: resolutionStrategy, Binary: binary,
	}, version)
	if jsonOut {
		if err := writeRepoJSON(stdout, report); err != nil {
			return err
		}
	} else {
		fmt.Fprintf(stdout, "Repository: %s\n", report.RepoRoot)
		fmt.Fprintf(stdout, "Status: %s\n", report.Status)
		fmt.Fprintf(stdout, "Resolution: %s %s\n", report.Strategy, report.Path)
		fmt.Fprintf(stdout, "Changed: %s\n", displayBootstrapList(report.Changed))
		fmt.Fprintf(stdout, "Rolled back: %s\n", displayBootstrapList(report.RolledBack))
		fmt.Fprintf(stdout, "Next: %s\n", report.NextAction)
	}
	if resolveErr != nil {
		return repoCLIError(resolveErr.Error())
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
		passes := 0
		failures := 0
		for _, check := range verification.Checks {
			if check.Status == "PASS" {
				passes++
				continue
			}
			failures++
			fmt.Fprintf(stdout, "FAIL %s: %s\n", check.Name, check.Detail)
		}
		fmt.Fprintf(stdout, "Checks: %d PASS, %d FAIL\n", passes, failures)
		fmt.Fprintf(stdout, "Next: %s\n", verification.NextAction)
	}
	if !verification.Valid {
		return &CLIError{ExitCode: 1, Message: ""}
	}
	return nil
}

func runRepoSyncRecover(args []string, stdout io.Writer) error {
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
	recovery, err := reconbootstrap.RecoverRepositorySync(repo)
	if jsonOut {
		if encodeErr := writeRepoJSON(stdout, recovery); encodeErr != nil {
			return encodeErr
		}
	} else {
		fmt.Fprintf(stdout, "Repository: %s\n", recovery.RepoRoot)
		fmt.Fprintf(stdout, "Status: %s\n", recovery.Status)
		if recovery.PlanDigest != "" {
			fmt.Fprintf(stdout, "Plan digest: %s\n", recovery.PlanDigest)
		}
		fmt.Fprintf(stdout, "Restored: %s\n", displayBootstrapList(recovery.Restored))
		fmt.Fprintf(stdout, "Next: %s\n", recovery.NextAction)
	}
	if err != nil {
		return repoCLIError(err.Error())
	}
	return nil
}

func renderRepoSyncPlan(stdout io.Writer, plan *reconbootstrap.SyncPlan, output, writeState string) {
	fmt.Fprintf(stdout, "Repository: %s\n", plan.RepoRoot)
	fmt.Fprintf(stdout, "Product: %s -> %s\n", plan.CurrentProductVersion, plan.TargetProductVersion)
	fmt.Fprintf(stdout, "Policy packs: %s -> %s\n", policyPackSummary(plan.CurrentPolicyPacks), policyPackSummary(plan.TargetPolicyPacks))
	fmt.Fprintf(stdout, "Harness packs: %s -> %s\n", harnessPackSummary(plan.CurrentHarnessPacks), harnessPackSummary(plan.TargetHarnessPacks))
	fmt.Fprintf(stdout, "Receipt: %s\n", plan.CurrentReceiptDigest)
	fmt.Fprintf(stdout, "Plan digest: %s\n", plan.PlanDigest)
	if output != "" {
		fmt.Fprintf(stdout, "Plan file: %s (%s)\n", output, writeState)
	}
	unchanged := 0
	for _, action := range plan.Actions {
		if action.State == reconbootstrap.SyncUnchanged {
			unchanged++
			continue
		}
		prefix := "ACTION"
		if action.State == reconbootstrap.SyncUserDrift ||
			action.State == reconbootstrap.SyncOrphanedLegacy ||
			action.State == reconbootstrap.SyncIncompatible ||
			action.State == reconbootstrap.SyncManualReview {
			prefix = "BLOCKED"
		}
		fmt.Fprintf(stdout, "%s %s %s: %s\n", prefix, action.State, action.Path, action.Reason)
	}
	fmt.Fprintf(stdout, "Unchanged: %d\n", unchanged)
	if len(plan.Migrations) > 0 {
		fmt.Fprintf(stdout, "Migrations: %d\n", len(plan.Migrations))
	}
	if output == "" {
		suggested := filepath.Join(os.TempDir(), "reconc-sync-"+plan.PlanDigest[:12]+".json")
		fmt.Fprintf(stdout, "Next: %s\n", renderDirectCommand([]string{
			"reconc", "repo", "sync", "plan", plan.RepoRoot,
			"--output", suggested,
		}))
	} else if len(plan.BlockingIssues) == 0 {
		fmt.Fprintf(stdout, "Next: %s\n", renderDirectCommand([]string{
			"reconc", "repo", "sync", "apply", "--plan", output,
			"--digest", plan.PlanDigest,
		}))
	} else {
		for _, action := range plan.Actions {
			if action.State == reconbootstrap.SyncUnchanged ||
				action.State == reconbootstrap.SyncReplaceOwned ||
				action.State == reconbootstrap.SyncUpdateManagedBlock ||
				action.State == reconbootstrap.SyncCreateOwned {
				continue
			}
			strategy := string(reconbootstrap.SyncKeepCurrent)
			if action.DesiredSHA256 != "" {
				strategy = string(reconbootstrap.SyncUseTarget)
			}
			fmt.Fprintf(stdout, "Next: %s\n", renderDirectCommand([]string{
				"reconc", "repo", "sync", "resolve",
				"--plan", output, "--digest", plan.PlanDigest,
				"--path", action.Path, "--strategy", strategy,
			}))
			break
		}
	}
}

func renderRepoSyncReport(stdout io.Writer, report *reconbootstrap.SyncReport) {
	fmt.Fprintf(stdout, "Repository: %s\n", report.RepoRoot)
	fmt.Fprintf(stdout, "Status: %s\n", report.Status)
	fmt.Fprintf(stdout, "Product: %s -> %s\n", report.ProductFrom, report.ProductTo)
	fmt.Fprintf(stdout, "Receipt: %s -> %s\n", report.ReceiptFrom, report.ReceiptTo)
	fmt.Fprintf(stdout, "Changed: %s\n", repoPathSummary(report.Changed))
	fmt.Fprintf(stdout, "Unchanged: %d\n", len(report.Unchanged))
	fmt.Fprintf(stdout, "Candidates: %s\n", repoPathSummary(report.Candidates))
	fmt.Fprintf(stdout, "Rolled back: %s\n", repoPathSummary(report.RolledBack))
	fmt.Fprintf(stdout, "Next: %s\n", report.NextAction)
}

func policyPackSummary(packs []reconbootstrap.PolicyPackIdentity) string {
	if len(packs) == 0 {
		return "none"
	}
	values := make([]string, len(packs))
	for index, pack := range packs {
		values[index] = pack.Name + "#" + pack.Digest[:12]
	}
	return strings.Join(values, ", ")
}

func harnessPackSummary(packs []reconbootstrap.HarnessPackIdentity) string {
	if len(packs) == 0 {
		return "none"
	}
	values := make([]string, len(packs))
	for index, pack := range packs {
		values[index] = pack.Name + "@" + pack.Version + "#" + pack.Digest[:12]
	}
	return strings.Join(values, ", ")
}

func repoPathSummary(paths []string) string {
	if len(paths) == 0 {
		return "none"
	}
	if len(paths) <= 8 {
		return strings.Join(paths, ", ")
	}
	return fmt.Sprintf("%d paths (%s, ...)", len(paths), strings.Join(paths[:3], ", "))
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
