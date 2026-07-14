package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"runtime"
	"strings"

	reconbootstrap "reconc.dev/reconc/internal/bootstrap"
)

type bootstrapRequestFlags struct {
	repo          string
	profile       reconbootstrap.ProfileName
	profileSet    bool
	packs         []string
	hooks         []string
	installBinary bool
	binaryPath    string
	binarySet     bool
	checksum      string
	checksumSet   bool
	platform      string
	platformSet   bool
	output        string
	outputSet     bool
	jsonOut       bool
	help          bool
}

func runBootstrap(args []string, version string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return runBootstrapLegacy(args, version, stdout, stderr)
	}
	switch args[0] {
	case "-h", "--help":
		printBootstrapHelp(stdout)
		return nil
	case "profiles":
		return runBootstrapProfiles(args[1:], stdout)
	case "inspect":
		return runBootstrapInspect(args[1:], stdout)
	case "plan":
		return runBootstrapPlan(args[1:], version, stdout)
	case "apply":
		return runBootstrapApply(args[1:], version, stdout)
	case "verify":
		return runBootstrapVerify(args[1:], stdout)
	default:
		return runBootstrapLegacy(args, version, stdout, stderr)
	}
}

func printBootstrapHelp(stdout io.Writer) {
	fmt.Fprintln(stdout, "Usage: reconc bootstrap inspect [repo] [--json]")
	fmt.Fprintln(stdout, "       reconc bootstrap profiles [--json]")
	fmt.Fprintln(stdout, "       reconc bootstrap plan [repo] --profile minimal|governed [selection flags]")
	fmt.Fprintln(stdout, "       reconc bootstrap apply --plan PATH [--json]")
	fmt.Fprintln(stdout, "       reconc bootstrap apply [repo] --profile minimal|governed [selection flags]")
	fmt.Fprintln(stdout, "       reconc bootstrap verify --plan PATH [--json]")
	fmt.Fprintln(stdout, "")
	fmt.Fprintln(stdout, "Selection flags:")
	fmt.Fprintln(stdout, "  --pack NAME             Add an applicable policy pack; repeatable")
	fmt.Fprintln(stdout, "  --hook KIND             Add a registered hook kind; repeatable; 'all' expands")
	fmt.Fprintln(stdout, "  --install-binary        Install the running Reconc binary for this platform")
	fmt.Fprintln(stdout, "  --binary PATH           Install an explicit local binary artifact")
	fmt.Fprintln(stdout, "  --checksum SHA256       Required with --binary")
	fmt.Fprintln(stdout, "  --platform OS/ARCH      Target for --binary; defaults to the running platform")
	fmt.Fprintln(stdout, "  --output PATH           Create the deterministic plan file; plan only")
	fmt.Fprintln(stdout, "  --json                  Emit deterministic machine-readable output")
	fmt.Fprintln(stdout, "")
	fmt.Fprintln(stdout, "Compatibility: `reconc bootstrap [repo]` runs a create-only minimal transaction with detected hooks.")
}

func runBootstrapProfiles(args []string, stdout io.Writer) error {
	jsonOut := false
	for _, arg := range args {
		switch arg {
		case "--json":
			jsonOut = true
		case "-h", "--help":
			fmt.Fprintln(stdout, "Usage: reconc bootstrap profiles [--json]")
			return nil
		default:
			return bootstrapCLIError("profiles", fmt.Sprintf("unknown argument %q", arg))
		}
	}
	profiles := reconbootstrap.Profiles()
	if jsonOut {
		return writeBootstrapJSON("profiles", stdout, profiles)
	}
	for _, profile := range profiles {
		fmt.Fprintf(stdout, "%s: %s\n", profile.Name, profile.Summary)
		fmt.Fprintf(stdout, "  default packs: %s\n", strings.Join(profile.DefaultPacks, ", "))
	}
	return nil
}

func runBootstrapInspect(args []string, stdout io.Writer) error {
	repo := "."
	repoSet := false
	jsonOut := false
	for _, arg := range args {
		switch arg {
		case "--json":
			jsonOut = true
		case "-h", "--help":
			fmt.Fprintln(stdout, "Usage: reconc bootstrap inspect [repo] [--json]")
			return nil
		default:
			if strings.HasPrefix(arg, "-") {
				return bootstrapCLIError("inspect", fmt.Sprintf("unknown flag %q", arg))
			}
			if repoSet {
				return bootstrapCLIError("inspect", fmt.Sprintf("unexpected argument %q", arg))
			}
			repo = arg
			repoSet = true
		}
	}
	inspection, err := reconbootstrap.Inspect(repo)
	if err != nil {
		return bootstrapCLIError("inspect", err.Error())
	}
	if jsonOut {
		return writeBootstrapJSON("inspect", stdout, inspection)
	}
	fmt.Fprintf(stdout, "Repository: %s\n", inspection.RepoRoot)
	fmt.Fprintf(stdout, "Stacks: %s\n", displayBootstrapList(inspection.DetectedStacks))
	fmt.Fprintf(stdout, "Suggested packs: %s\n", displayBootstrapList(inspection.PackSuggestions))
	fmt.Fprintf(stdout, "Detected platforms: %s\n", displayBootstrapList(inspection.DetectedPlatforms))
	fmt.Fprintf(stdout, "Existing control paths: %s\n", displayBootstrapList(inspection.ExistingPaths))
	if inspection.BinaryResolution.Path != "" {
		fmt.Fprintf(stdout, "Binary: %s (%s)\n", inspection.BinaryResolution.Path, inspection.BinaryResolution.Source)
	} else {
		fmt.Fprintf(stdout, "Binary: unavailable (%s)\n", inspection.BinaryResolution.Diagnostic)
	}
	return nil
}

func runBootstrapPlan(args []string, version string, stdout io.Writer) error {
	flags, err := parseBootstrapRequestFlags("plan", args, true)
	if err != nil {
		return err
	}
	if flags.help {
		printBootstrapHelp(stdout)
		return nil
	}
	request, err := flags.request()
	if err != nil {
		return bootstrapCLIError("plan", err.Error())
	}
	plan, err := reconbootstrap.BuildPlan(request, version)
	if err != nil {
		return bootstrapCLIError("plan", err.Error())
	}
	writeState := ""
	if flags.output != "" {
		writeState, err = reconbootstrap.WritePlan(flags.output, plan)
		if err != nil {
			return bootstrapCLIError("plan", err.Error())
		}
	}
	if flags.jsonOut {
		if err := writeBootstrapJSON("plan", stdout, plan); err != nil {
			return err
		}
	} else {
		renderBootstrapPlan(stdout, plan, flags.output, writeState)
	}
	if len(plan.BlockingIssues) > 0 {
		return &CLIError{ExitCode: 1, Message: ""}
	}
	return nil
}

func runBootstrapApply(args []string, version string, stdout io.Writer) error {
	planPath := ""
	planSet := false
	jsonOut := false
	selectionArgs := []string{}
	for index := 0; index < len(args); index++ {
		switch args[index] {
		case "--plan":
			if planSet {
				return bootstrapCLIError("apply", "--plan may be specified only once")
			}
			value, ok := nextArgValue(args, &index, "--plan")
			if !ok {
				return bootstrapCLIError("apply", "--plan requires a path")
			}
			planPath = value
			planSet = true
		case "--json":
			jsonOut = true
		case "-h", "--help":
			printBootstrapHelp(stdout)
			return nil
		default:
			selectionArgs = append(selectionArgs, args[index])
		}
	}
	var plan *reconbootstrap.Plan
	var err error
	if planPath != "" {
		if len(selectionArgs) > 0 {
			return bootstrapCLIError("apply", "--plan cannot be combined with repository or selection flags")
		}
		plan, err = reconbootstrap.LoadPlan(planPath)
	} else {
		flags, parseErr := parseBootstrapRequestFlags("apply", selectionArgs, false)
		if parseErr != nil {
			return parseErr
		}
		if flags.jsonOut {
			jsonOut = true
		}
		request, requestErr := flags.request()
		if requestErr != nil {
			return bootstrapCLIError("apply", requestErr.Error())
		}
		plan, err = reconbootstrap.BuildPlan(request, version)
	}
	if err != nil {
		return bootstrapCLIError("apply", err.Error())
	}
	report, applyErr := reconbootstrap.Apply(plan, version)
	if applyErr != nil {
		if report != nil {
			report.NextAction = applyErr.Error()
			if jsonOut {
				if err := writeBootstrapJSON("apply", stdout, report); err != nil {
					return err
				}
			} else {
				renderBootstrapReport(stdout, report)
			}
		}
		return bootstrapCLIError("apply", applyErr.Error())
	}
	if jsonOut {
		if err := writeBootstrapJSON("apply", stdout, report); err != nil {
			return err
		}
	} else {
		renderBootstrapReport(stdout, report)
	}
	if report.Status != reconbootstrap.ApplyComplete {
		return &CLIError{ExitCode: 1, Message: ""}
	}
	return nil
}

func runBootstrapVerify(args []string, stdout io.Writer) error {
	planPath := ""
	planSet := false
	jsonOut := false
	for index := 0; index < len(args); index++ {
		switch args[index] {
		case "--plan":
			if planSet {
				return bootstrapCLIError("verify", "--plan may be specified only once")
			}
			value, ok := nextArgValue(args, &index, "--plan")
			if !ok {
				return bootstrapCLIError("verify", "--plan requires a path")
			}
			planPath = value
			planSet = true
		case "--json":
			jsonOut = true
		case "-h", "--help":
			fmt.Fprintln(stdout, "Usage: reconc bootstrap verify --plan PATH [--json]")
			return nil
		default:
			return bootstrapCLIError("verify", fmt.Sprintf("unknown argument %q", args[index]))
		}
	}
	if planPath == "" {
		return bootstrapCLIError("verify", "--plan is required")
	}
	plan, err := reconbootstrap.LoadPlan(planPath)
	if err != nil {
		return bootstrapCLIError("verify", err.Error())
	}
	verification, err := reconbootstrap.Verify(plan)
	if err != nil {
		return bootstrapCLIError("verify", err.Error())
	}
	if jsonOut {
		if err := writeBootstrapJSON("verify", stdout, verification); err != nil {
			return err
		}
	} else {
		renderBootstrapVerification(stdout, verification)
	}
	if !verification.Valid {
		return &CLIError{ExitCode: 1, Message: ""}
	}
	return nil
}

func parseBootstrapRequestFlags(command string, args []string, allowOutput bool) (bootstrapRequestFlags, error) {
	flags := bootstrapRequestFlags{repo: "."}
	repoSet := false
	for index := 0; index < len(args); index++ {
		arg := args[index]
		switch arg {
		case "--profile":
			if flags.profileSet {
				return flags, bootstrapCLIError(command, "--profile may be specified only once")
			}
			value, ok := nextArgValue(args, &index, arg)
			if !ok {
				return flags, bootstrapCLIError(command, "--profile requires a value")
			}
			flags.profile = reconbootstrap.ProfileName(value)
			flags.profileSet = true
		case "--pack":
			value, ok := nextArgValue(args, &index, arg)
			if !ok {
				return flags, bootstrapCLIError(command, "--pack requires a value")
			}
			flags.packs = append(flags.packs, value)
		case "--hook":
			value, ok := nextArgValue(args, &index, arg)
			if !ok {
				return flags, bootstrapCLIError(command, "--hook requires a value")
			}
			flags.hooks = append(flags.hooks, value)
		case "--install-binary":
			flags.installBinary = true
		case "--binary":
			if flags.binarySet {
				return flags, bootstrapCLIError(command, "--binary may be specified only once")
			}
			value, ok := nextArgValue(args, &index, arg)
			if !ok {
				return flags, bootstrapCLIError(command, "--binary requires a path")
			}
			flags.binaryPath = value
			flags.binarySet = true
		case "--checksum":
			if flags.checksumSet {
				return flags, bootstrapCLIError(command, "--checksum may be specified only once")
			}
			value, ok := nextArgValue(args, &index, arg)
			if !ok {
				return flags, bootstrapCLIError(command, "--checksum requires a SHA-256 value")
			}
			flags.checksum = value
			flags.checksumSet = true
		case "--platform":
			if flags.platformSet {
				return flags, bootstrapCLIError(command, "--platform may be specified only once")
			}
			value, ok := nextArgValue(args, &index, arg)
			if !ok {
				return flags, bootstrapCLIError(command, "--platform requires OS/ARCH")
			}
			flags.platform = value
			flags.platformSet = true
		case "--output":
			if !allowOutput {
				return flags, bootstrapCLIError(command, "--output is supported only by bootstrap plan")
			}
			if flags.outputSet {
				return flags, bootstrapCLIError(command, "--output may be specified only once")
			}
			value, ok := nextArgValue(args, &index, arg)
			if !ok {
				return flags, bootstrapCLIError(command, "--output requires a path")
			}
			flags.output = value
			flags.outputSet = true
		case "--json":
			flags.jsonOut = true
		case "-h", "--help":
			flags.help = true
		default:
			if strings.HasPrefix(arg, "-") {
				return flags, bootstrapCLIError(command, fmt.Sprintf("unknown flag %q", arg))
			}
			if repoSet {
				return flags, bootstrapCLIError(command, fmt.Sprintf("unexpected argument %q", arg))
			}
			flags.repo = arg
			repoSet = true
		}
	}
	return flags, nil
}

func (flags bootstrapRequestFlags) request() (reconbootstrap.Request, error) {
	if !flags.profileSet {
		return reconbootstrap.Request{}, fmt.Errorf("--profile is required; choose minimal or governed after inspect")
	}
	if flags.installBinary && flags.binaryPath != "" {
		return reconbootstrap.Request{}, fmt.Errorf("--install-binary and --binary are mutually exclusive")
	}
	if flags.installBinary && (flags.checksum != "" || flags.platform != "") {
		return reconbootstrap.Request{}, fmt.Errorf("--checksum and --platform apply only to --binary")
	}
	if flags.binaryPath == "" && flags.checksum != "" {
		return reconbootstrap.Request{}, fmt.Errorf("--checksum requires --binary")
	}
	if flags.binaryPath == "" && flags.platform != "" {
		return reconbootstrap.Request{}, fmt.Errorf("--platform requires --binary")
	}
	var binary *reconbootstrap.BinarySelection
	var err error
	if flags.installBinary {
		binary, err = reconbootstrap.CurrentBinarySelection()
	} else if flags.binaryPath != "" {
		if flags.checksum == "" {
			return reconbootstrap.Request{}, fmt.Errorf("--binary requires --checksum")
		}
		targetOS := runtime.GOOS
		targetArch := runtime.GOARCH
		if flags.platform != "" {
			parts := strings.Split(flags.platform, "/")
			if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
				return reconbootstrap.Request{}, fmt.Errorf("--platform must be exactly OS/ARCH")
			}
			targetOS = parts[0]
			targetArch = parts[1]
		}
		binary, err = reconbootstrap.BinarySelectionFor(flags.binaryPath, flags.checksum, targetOS, targetArch)
	}
	if err != nil {
		return reconbootstrap.Request{}, err
	}
	return reconbootstrap.Request{
		RepoRoot: flags.repo, Profile: flags.profile, Packs: flags.packs,
		Hooks: flags.hooks, Binary: binary,
	}, nil
}

func renderBootstrapPlan(stdout io.Writer, plan *reconbootstrap.Plan, output, writeState string) {
	fmt.Fprintf(stdout, "Bootstrap plan: %s\n", plan.PlanDigest)
	fmt.Fprintf(stdout, "Repository: %s\n", plan.RepoRoot)
	fmt.Fprintf(stdout, "Profile: %s\n", plan.Selection.Profile)
	fmt.Fprintf(stdout, "Packs: %s\n", displayBootstrapList(plan.Selection.Packs))
	fmt.Fprintf(stdout, "Hooks: %s\n", displayBootstrapList(plan.Selection.Hooks))
	for _, action := range plan.Actions {
		fmt.Fprintf(stdout, "  %s  %s  (%s)\n", action.State, action.Path, action.Component)
		if action.CandidatePath != "" {
			fmt.Fprintf(stdout, "    candidate: %s\n", action.CandidatePath)
		}
	}
	for _, issue := range plan.BlockingIssues {
		fmt.Fprintf(stdout, "Blocking issue: %s\n", issue)
	}
	if output != "" {
		fmt.Fprintf(stdout, "Plan file: %s (%s)\n", output, writeState)
	}
}

func renderBootstrapReport(stdout io.Writer, report *reconbootstrap.Report) {
	fmt.Fprintf(stdout, "Bootstrap apply: %s\n", report.Status)
	fmt.Fprintf(stdout, "Repository: %s\n", report.RepoRoot)
	fmt.Fprintf(stdout, "Created: %s\n", displayBootstrapList(report.Created))
	fmt.Fprintf(stdout, "Unchanged: %s\n", displayBootstrapList(report.Unchanged))
	fmt.Fprintf(stdout, "Candidates: %s\n", displayBootstrapList(report.Candidates))
	fmt.Fprintf(stdout, "Rolled back: %s\n", displayBootstrapList(report.RolledBack))
	fmt.Fprintf(stdout, "Next: %s\n", report.NextAction)
}

func renderBootstrapVerification(stdout io.Writer, verification *reconbootstrap.Verification) {
	fmt.Fprintf(stdout, "Bootstrap verify: valid=%t\n", verification.Valid)
	fmt.Fprintf(stdout, "Repository: %s\n", verification.RepoRoot)
	for _, check := range verification.Checks {
		fmt.Fprintf(stdout, "  %s  %s: %s\n", check.Status, check.Name, check.Detail)
	}
	fmt.Fprintf(stdout, "Next: %s\n", verification.NextAction)
}

func writeBootstrapJSON(command string, stdout io.Writer, value interface{}) error {
	encoder := json.NewEncoder(stdout)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(value); err != nil {
		return bootstrapCLIError(command, "json encode: "+err.Error())
	}
	return nil
}

func bootstrapCLIError(command, message string) error {
	return &CLIError{ExitCode: 1, Message: "reconc bootstrap " + command + ": " + message}
}

func displayBootstrapList(values []string) string {
	if len(values) == 0 {
		return "none"
	}
	return strings.Join(values, ", ")
}

func containsString(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}
