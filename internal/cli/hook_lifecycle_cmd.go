package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"reconc.dev/reconc/internal/hooks"
)

func runHookGenerate(args []string, stdout, stderr io.Writer) (resultErr error) {
	if len(args) == 0 {
		return &CLIError{ExitCode: 1, Message: fmt.Sprintf("reconc hook generate: missing kind (one of: %v)", hooks.SupportedKinds())}
	}
	if args[0] == "-h" || args[0] == "--help" {
		fmt.Fprintf(stdout, "Usage: reconc hook generate <kind> [--json] [--output PATH]\nKinds: %v\n", hooks.SupportedKinds())
		return nil
	}
	kind := args[0]
	jsonOut := false
	outputPath := ""
	i := 1
	for i < len(args) {
		a := args[i]
		switch a {
		case "--json":
			jsonOut = true
		case "--output":
			val, ok := nextArgValue(args, &i, a)
			if !ok {
				return &CLIError{ExitCode: 1, Message: "reconc hook generate: --output requires a path"}
			}
			outputPath = val
		case "-h", "--help":
			fmt.Fprintf(stdout, "Usage: reconc hook generate <kind> [--json] [--output PATH]\nKinds: %v\n", hooks.SupportedKinds())
			return nil
		default:
			return &CLIError{ExitCode: 1, Message: fmt.Sprintf("reconc hook generate: unknown flag %q", a)}
		}
		i++
	}
	a, err := hooks.Generate(kind)
	if err != nil {
		return &CLIError{ExitCode: 1, Message: "reconc hook generate: " + err.Error()}
	}
	out, closeOutput, err := teeToFile(stdout, outputPath)
	if err != nil {
		return &CLIError{ExitCode: 1, Message: "reconc hook generate: open output file: " + err.Error()}
	}
	defer joinOutputCloseError(&resultErr, closeOutput)
	if jsonOut {
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		if err := enc.Encode(a); err != nil {
			return &CLIError{ExitCode: 1, Message: "reconc hook generate: json encode: " + err.Error()}
		}
		return nil
	}
	// Write the raw artifact content to stdout so users can redirect.
	fmt.Fprint(out, a.Content)
	return nil
}

func runHookInstall(args []string, stdout, stderr io.Writer) (resultErr error) {
	if len(args) == 0 {
		return &CLIError{ExitCode: 1, Message: fmt.Sprintf("reconc hook install: missing kind (one of: %v)", hooks.InstallableKinds())}
	}
	if args[0] == "-h" || args[0] == "--help" {
		fmt.Fprintf(stdout, "Usage: reconc hook install <kind> [repo] [--force] [--json] [--output PATH]\nInstallable: %v\n", hooks.InstallableKinds())
		return nil
	}
	kind := args[0]
	repo := "."
	repoSet := false
	force := false
	jsonOut := false
	outputPath := ""
	for i := 1; i < len(args); i++ {
		a := args[i]
		switch a {
		case "--json":
			jsonOut = true
		case "--force":
			force = true
		case "--output":
			val, ok := nextArgValue(args, &i, a)
			if !ok {
				return &CLIError{ExitCode: 1, Message: "reconc hook install: --output requires a path"}
			}
			outputPath = val
		case "-h", "--help":
			fmt.Fprintf(stdout, "Usage: reconc hook install <kind> [repo] [--force] [--json] [--output PATH]\nInstallable: %v\n", hooks.InstallableKinds())
			return nil
		default:
			if len(a) > 0 && a[0] == '-' {
				return &CLIError{ExitCode: 1, Message: fmt.Sprintf("reconc hook install: unknown flag %q", a)}
			}
			if repoSet {
				return &CLIError{ExitCode: 1, Message: "reconc hook install: expected at most one repo path"}
			}
			repo = a
			repoSet = true
		}
	}
	if kind == hooks.KindKimiCode && repoSet {
		return &CLIError{ExitCode: 1, Message: "reconc hook install: kimi-code is user-global and does not accept a repo path"}
	}
	report, installErr := hooks.Install(kind, repo, force)
	out, closeOutput, err := teeToFile(stdout, outputPath)
	if err != nil {
		return &CLIError{ExitCode: 1, Message: "reconc hook install: open output file: " + err.Error()}
	}
	defer joinOutputCloseError(&resultErr, closeOutput)
	if jsonOut {
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		response := struct {
			*hooks.InstallReport
			Success bool   `json:"success"`
			Error   string `json:"error,omitempty"`
		}{InstallReport: report, Success: installErr == nil}
		if installErr != nil {
			response.Error = installErr.Error()
		}
		if err := enc.Encode(response); err != nil {
			return &CLIError{ExitCode: 1, Message: "reconc hook install: json encode: " + err.Error()}
		}
	} else if report != nil {
		result := "Installed"
		if installErr != nil {
			result = "Install failed for"
			if report.Partial {
				result = "Partially installed"
			}
		}
		fmt.Fprintf(out, "%s %s hook (%s)\n", result, report.Kind, report.Action)
		scopeLabel := "Repo"
		if report.RepoRoot == "global" {
			scopeLabel = "Scope"
		}
		fmt.Fprintf(out, "%-8s %s\n", scopeLabel+":", report.RepoRoot)
		fmt.Fprintf(out, "Target:  %s\n", report.TargetPath)
		if report.WrapperPath != "" {
			fmt.Fprintf(out, "Wrapper: %s (%s)\n", report.WrapperPath, report.WrapperAction)
		}
		if report.ActivationPath != "" {
			fmt.Fprintf(out, "Activate: %s (%s)\n", report.ActivationPath, report.ActivationAction)
		}
		if installErr != nil {
			fmt.Fprintln(out, "Status:  failed")
			fmt.Fprintf(out, "Error:   %s\n", installErr)
		}
		fmt.Fprintf(out, "Next:    %s\n", report.NextAction)
	} else if installErr != nil {
		fmt.Fprintln(out, "Hook installation failed")
		fmt.Fprintln(out, "Status:  failed")
		fmt.Fprintf(out, "Error:   %s\n", installErr)
	}
	if installErr != nil {
		return commitOutput(closeOutput, &CLIError{ExitCode: 1, Message: "reconc hook install: " + installErr.Error()})
	}
	// Surface any user-modified reconc entries that got overwritten so
	// operators notice.
	if len(report.DroppedUserEdits) > 0 {
		fmt.Fprintf(stderr, "reconc hook install: replaced %d user-modified reconc entr(y/ies):\n",
			len(report.DroppedUserEdits))
		for _, e := range report.DroppedUserEdits {
			fmt.Fprintf(stderr, "  - %s\n", e)
		}
		fmt.Fprintln(stderr, "  (If this was intentional, redo the edit via a wrapper command)")
	}
	if report.BackupPath != "" {
		fmt.Fprintf(stderr, "reconc hook install: replaced managed configuration; original preserved at %s\n",
			report.BackupPath)
	}
	return nil
}

func runHookUninstall(args []string, stdout io.Writer) (resultErr error) {
	if len(args) == 0 {
		return &CLIError{ExitCode: 1, Message: fmt.Sprintf("reconc hook uninstall: missing kind (one of: %v)", hooks.InstallableKinds())}
	}
	if args[0] == "-h" || args[0] == "--help" {
		fmt.Fprintf(stdout, "Usage: reconc hook uninstall <kind> [repo] [--json] [--output PATH]\nUninstallable: %v\n", hooks.InstallableKinds())
		return nil
	}
	kind := args[0]
	repo := "."
	repoSet := false
	jsonOut := false
	outputPath := ""
	for index := 1; index < len(args); index++ {
		arg := args[index]
		switch arg {
		case "--json":
			jsonOut = true
		case "--output":
			value, ok := nextArgValue(args, &index, arg)
			if !ok {
				return &CLIError{ExitCode: 1, Message: "reconc hook uninstall: --output requires a path"}
			}
			outputPath = value
		case "-h", "--help":
			fmt.Fprintf(stdout, "Usage: reconc hook uninstall <kind> [repo] [--json] [--output PATH]\nUninstallable: %v\n", hooks.InstallableKinds())
			return nil
		default:
			if strings.HasPrefix(arg, "-") {
				return &CLIError{ExitCode: 1, Message: fmt.Sprintf("reconc hook uninstall: unknown flag %q", arg)}
			}
			if repoSet {
				return &CLIError{ExitCode: 1, Message: "reconc hook uninstall: accepts at most one repo path"}
			}
			repo = arg
			repoSet = true
		}
	}
	if kind == hooks.KindKimiCode && repoSet {
		return &CLIError{ExitCode: 1, Message: "reconc hook uninstall: kimi-code is user-global and does not accept a repo path"}
	}
	report, err := hooks.Uninstall(kind, repo)
	if err != nil {
		return &CLIError{ExitCode: 1, Message: "reconc hook uninstall: " + err.Error()}
	}
	out, closeOutput, err := teeToFile(stdout, outputPath)
	if err != nil {
		return &CLIError{ExitCode: 1, Message: "reconc hook uninstall: open output file: " + err.Error()}
	}
	defer joinOutputCloseError(&resultErr, closeOutput)
	if jsonOut {
		encoder := json.NewEncoder(out)
		encoder.SetIndent("", "  ")
		if err := encoder.Encode(report); err != nil {
			return &CLIError{ExitCode: 1, Message: "reconc hook uninstall: json encode: " + err.Error()}
		}
		return nil
	}
	fmt.Fprintf(out, "Uninstalled %s hook (%s)\n", report.Kind, report.Action)
	scopeLabel := "Repo"
	if report.RepoRoot == "global" {
		scopeLabel = "Scope"
	}
	fmt.Fprintf(out, "%-11s %s\n", scopeLabel+":", report.RepoRoot)
	fmt.Fprintf(out, "Target:     %s\n", report.TargetPath)
	fmt.Fprintf(out, "Entries:    %d removed\n", report.RemovedEntries)
	if report.ActivationAction != "" {
		fmt.Fprintf(out, "Activation: %s\n", report.ActivationAction)
	}
	if report.WrapperPath != "" {
		fmt.Fprintf(out, "Wrapper:    %s (%s)\n", report.WrapperPath, report.WrapperAction)
	}
	fmt.Fprintf(out, "Next:       %s\n", report.NextAction)
	return nil
}

func runHookSyncScaffold(args []string, stdout, stderr io.Writer) error {
	scaffoldRoot := ""
	jsonOut := false
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch a {
		case "--json":
			jsonOut = true
		case "-h", "--help":
			fmt.Fprintln(stdout, "Usage: reconc hook sync-scaffold <repo-root-scaffold> [--json]")
			fmt.Fprintln(stdout, "Writes every registry-managed hook artifact into a repo-root scaffold.")
			return nil
		default:
			if len(a) > 0 && a[0] == '-' {
				return &CLIError{ExitCode: 1, Message: fmt.Sprintf("reconc hook sync-scaffold: unknown flag %q", a)}
			}
			if scaffoldRoot != "" {
				return &CLIError{ExitCode: 1, Message: "reconc hook sync-scaffold: accepts exactly one scaffold path"}
			}
			scaffoldRoot = a
		}
	}
	if scaffoldRoot == "" {
		return &CLIError{ExitCode: 1, Message: "reconc hook sync-scaffold: missing repo-root-scaffold path"}
	}
	report, err := hooks.SyncRepoRootScaffold(scaffoldRoot)
	if err != nil {
		return &CLIError{ExitCode: 1, Message: "reconc hook sync-scaffold: " + err.Error()}
	}
	if jsonOut {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(report); err != nil {
			return &CLIError{ExitCode: 1, Message: "reconc hook sync-scaffold: json encode: " + err.Error()}
		}
		return nil
	}
	fmt.Fprintf(stdout, "Synced hook scaffold: %s\n", report.ScaffoldRoot)
	for _, artifact := range report.Artifacts {
		fmt.Fprintf(stdout, "- %s: %s -> %s\n", artifact.Kind, artifact.Action, artifact.TargetPath)
	}
	return nil
}

// runHookRuntime dispatches `reconc hook runtime <event> <repo>` to
// the agent-session adapter. Reads a JSON payload from stdin, runs
// the per-event handler, and translates the Result into exit code +
// stdout/stderr.
//
// dedupToFirstClassRoute suppresses a cross-runtime duplicate event only
// when the first-class platform is fully configured and executable in this
// repository. Without that gate, a stray environment variable
// (DEVIN_PROJECT_DIR) or overlapping payload field names could silently
// no-op the ONLY enforcement route in arbitrary repos. The dedup is
// recorded (stderr note + liveness) so `reconc hook status` reflects
// activity instead of showing dead routes.
