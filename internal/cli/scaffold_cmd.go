package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"

	reconbootstrap "reconc.dev/reconc/internal/bootstrap"
)

type initCLIOptions struct {
	request    reconbootstrap.InitRequest
	jsonOut    bool
	outputPath string
}

func runInit(args []string, version string, stdout, stderr io.Writer) error {
	options, help, err := parseInitOptions(args)
	if err != nil {
		return err
	}
	if help {
		printInitHelp(stdout)
		return nil
	}
	return runInitOperation(options, version, stdout, stderr)
}

func parseInitOptions(args []string) (initCLIOptions, bool, error) {
	options := initCLIOptions{request: reconbootstrap.InitRequest{RepoRoot: "."}}
	repoSet := false
	profileSet := false
	outputSet := false
	for index := 0; index < len(args); index++ {
		arg := args[index]
		switch arg {
		case "--json":
			options.jsonOut = true
		case "--output":
			if outputSet {
				return options, false, initCLIError("--output may be specified only once")
			}
			value, ok := nextArgValue(args, &index, arg)
			if !ok {
				return options, false, initCLIError("--output requires a path")
			}
			options.outputPath = value
			outputSet = true
		case "--profile":
			if profileSet {
				return options, false, initCLIError("--profile may be specified only once")
			}
			value, ok := nextArgValue(args, &index, arg)
			if !ok {
				return options, false, initCLIError("--profile requires a value")
			}
			options.request.Profile = reconbootstrap.ProfileName(value)
			profileSet = true
		case "--pack":
			value, ok := nextArgValue(args, &index, arg)
			if !ok {
				return options, false, initCLIError("--pack requires a value")
			}
			options.request.Packs = append(options.request.Packs, value)
		case "--hook":
			value, ok := nextArgValue(args, &index, arg)
			if !ok {
				return options, false, initCLIError("--hook requires a value")
			}
			options.request.Hooks = append(options.request.Hooks, value)
			options.request.HooksExplicit = true
		case "--no-hooks":
			options.request.NoHooks = true
		case "--accept-managed-blocks":
			options.request.AcceptManagedBlocks = true
		case "--force":
			return options, false, initCLIError("--force is unsupported; Reconc never overwrites user-owned content. Review hash-addressed candidates or use --accept-managed-blocks for byte-verified marker-only updates")
		case "--preset":
			value, ok := nextArgValue(args, &index, arg)
			if !ok {
				return options, false, initCLIError("--preset requires a value")
			}
			options.request.Packs = append(options.request.Packs, value)
			options.request.CompatibilityWarning = append(options.request.CompatibilityWarning,
				"--preset is deprecated and was mapped to --pack; use --pack on the next run")
		case "-h", "--help":
			return options, true, nil
		default:
			if strings.HasPrefix(arg, "-") {
				return options, false, initCLIError(fmt.Sprintf("unknown flag %q", arg))
			}
			if repoSet {
				return options, false, initCLIError(fmt.Sprintf("unexpected argument %q", arg))
			}
			options.request.RepoRoot = arg
			repoSet = true
		}
	}
	if options.request.NoHooks && options.request.HooksExplicit {
		return options, false, initCLIError("--hook and --no-hooks are mutually exclusive")
	}
	return options, false, nil
}

func printInitHelp(stdout io.Writer) {
	fmt.Fprintln(stdout, "Usage: reconc init [repo] [--profile existing|minimal|governed|advanced]")
	fmt.Fprintln(stdout, "                   [--pack NAME ...] [--hook KIND ... | --no-hooks]")
	fmt.Fprintln(stdout, "                   [--accept-managed-blocks] [--json] [--output PATH]")
	fmt.Fprintln(stdout, "")
	fmt.Fprintln(stdout, "Canonical non-interactive repository onboarding.")
	fmt.Fprintln(stdout, "The command inspects, plans, applies, records, and verifies one create-only")
	fmt.Fprintln(stdout, "transaction. Existing user content is never overwritten.")
	fmt.Fprintln(stdout, "Fresh repositories default to minimal. Existing unreceipted control state")
	fmt.Fprintln(stdout, "requires an explicit profile and performs no repository write.")
}

func runInitOperation(options initCLIOptions, version string, stdout, stderr io.Writer) (resultErr error) {
	out, closeOutput, err := teeToFile(stdout, options.outputPath)
	if err != nil {
		return initCLIError("open output file: " + err.Error())
	}
	defer joinOutputCloseError(&resultErr, closeOutput)

	if err := ensureCurrentUserCLI("init", version); err != nil {
		report := failedInitReport(options.request.RepoRoot, version, err.Error())
		if renderErr := renderInitResult(out, report, options.jsonOut); renderErr != nil {
			return renderErr
		}
		return initResultError(options.jsonOut, err)
	}
	report, initErr := reconbootstrap.Initialize(options.request, version)
	if report != nil {
		report.Checks = append(report.Checks, reconbootstrap.Check{
			Name: "user-cli", Status: "PASS",
			Detail: "the running build is installed and directly callable as reconc",
		})
	}
	if err := renderInitResult(out, report, options.jsonOut); err != nil {
		return err
	}
	if initErr != nil {
		return initResultError(options.jsonOut, initErr)
	}
	if report.Status != reconbootstrap.InitComplete {
		return &CLIError{ExitCode: 1, Message: ""}
	}
	return nil
}

func renderInitResult(stdout io.Writer, report *reconbootstrap.InitReport, jsonOut bool) error {
	if report == nil {
		return initCLIError("internal error: init report is nil")
	}
	if jsonOut {
		encoder := json.NewEncoder(stdout)
		encoder.SetIndent("", "  ")
		if err := encoder.Encode(report); err != nil {
			return initCLIError("json encode: " + err.Error())
		}
		return nil
	}
	style := newTextStyler(stdout)
	fmt.Fprintf(stdout, "Reconc init: %s\n", style.decision(string(report.Status)))
	fmt.Fprintf(stdout, "Repository: %s\n", report.RepoRoot)
	if report.Profile != "" {
		fmt.Fprintf(stdout, "Profile: %s\n", report.Profile)
	}
	fmt.Fprintf(stdout, "Policy packs: %s\n", displayBootstrapList(report.PolicyPacks))
	fmt.Fprintf(stdout, "Harness packs: %s\n", displayHarnessPacks(report.HarnessPacks))
	fmt.Fprintf(stdout, "Hooks: %s\n", displayBootstrapList(report.Hooks))
	if report.PlanDigest != nil {
		fmt.Fprintf(stdout, "Plan: %s\n", *report.PlanDigest)
	}
	if report.PlanPath != nil {
		fmt.Fprintf(stdout, "Plan file: %s\n", *report.PlanPath)
	}
	if report.ReceiptPath != nil {
		fmt.Fprintf(stdout, "Receipt: %s\n", *report.ReceiptPath)
	}
	fmt.Fprintf(stdout, "Changed: %t\n", report.Changed)
	fmt.Fprintf(stdout, "Candidates: %s\n", displayBootstrapList(report.Candidates))
	for _, warning := range report.Warnings {
		fmt.Fprintf(stdout, "Warning: %s\n", warning)
	}
	for _, check := range report.Checks {
		fmt.Fprintf(stdout, "  %s  %s: %s\n", style.statusTag(check.Status, 4), check.Name, check.Detail)
	}
	fmt.Fprintf(stdout, "Next: %s\n", report.NextAction)
	return nil
}

func failedInitReport(repo, version, next string) *reconbootstrap.InitReport {
	return &reconbootstrap.InitReport{
		FormatVersion: reconbootstrap.InitFormatVersion, Operation: "init",
		Status: reconbootstrap.InitRefused, CurrentVersion: version, RepoRoot: repo,
		PolicyPacks: []string{}, Hooks: []string{}, Checks: []reconbootstrap.Check{},
		HarnessPacks: []reconbootstrap.HarnessPackSelection{},
		Actions:      []reconbootstrap.Action{}, Candidates: []string{}, Warnings: []string{},
		NextAction: next,
	}
}

func displayHarnessPacks(packs []reconbootstrap.HarnessPackSelection) string {
	if len(packs) == 0 {
		return "none"
	}
	values := make([]string, len(packs))
	for index, pack := range packs {
		values[index] = pack.Name + "@" + pack.Version + "#" + pack.Digest[:12]
	}
	return strings.Join(values, ", ")
}

func initResultError(jsonOut bool, err error) error {
	if jsonOut {
		return &CLIError{ExitCode: 1, Message: ""}
	}
	return initCLIError(err.Error())
}

func initCLIError(message string) error {
	return &CLIError{ExitCode: 1, Message: "reconc init: " + message}
}
