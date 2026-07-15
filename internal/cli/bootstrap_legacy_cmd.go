package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	reconbootstrap "reconc.dev/reconc/internal/bootstrap"
	"reconc.dev/reconc/internal/hooks"
	"reconc.dev/reconc/internal/scaffold"
	"strings"
)

// runBootstrapLegacy preserves the original one-command surface while using
// the same create-only transaction as the explicit bootstrap phases.
//
// Behavior:
//   - Existing files are never overwritten.
//   - Detected hooks are selected for the transaction unless skipped.
//   - Drift creates reviewed candidates and exits non-zero.
//
// Flags mirror init + extra ones for hook control:
//
//	--preset NAME (rep)    same as init
//	--force                rejected because bootstrap is create-only
//	--skip-git-hook        do not install .git/hooks/pre-commit
//	--json                 emit structured JSON instead of text
func runBootstrapLegacy(args []string, version string, stdout, stderr io.Writer) error {
	repo := "."
	jsonOut := false
	skipGitHook := false
	skipAgentHooks := false
	opts := scaffold.Options{}

	i := 0
	for i < len(args) {
		a := args[i]
		switch a {
		case "--json":
			jsonOut = true
		case "--force":
			opts.Force = true
		case "--skip-git-hook":
			skipGitHook = true
		case "--skip-agent-hooks":
			skipAgentHooks = true
		case "--preset":
			val, ok := nextArgValue(args, &i, a)
			if !ok {
				return &CLIError{ExitCode: 1, Message: "reconc bootstrap: --preset requires a value"}
			}
			opts.Presets = append(opts.Presets, val)
		case "-h", "--help":
			fmt.Fprintln(stdout, "Usage: reconc bootstrap [repo] [--preset NAME ...]")
			fmt.Fprintln(stdout, "                       [--skip-git-hook] [--skip-agent-hooks] [--json]")
			fmt.Fprintln(stdout, "")
			fmt.Fprintln(stdout, "Compatibility bootstrap: create-only minimal transaction with detected hooks.")
			fmt.Fprintln(stdout, "- git pre-commit is installed when .git/ is present.")
			fmt.Fprintln(stdout, "- Registered agent hooks are installed when their repo-local config dirs exist.")
			return nil
		default:
			if len(a) > 0 && a[0] == '-' {
				return &CLIError{ExitCode: 1, Message: fmt.Sprintf("reconc bootstrap: unknown flag %q", a)}
			}
			repo = a
		}
		i++
	}
	if opts.Force {
		return &CLIError{ExitCode: 1, Message: "reconc bootstrap: --force is unsupported; inspect drift and integrate candidate files surgically"}
	}

	inspection, err := reconbootstrap.Inspect(repo)
	if err != nil {
		return &CLIError{ExitCode: 1, Message: "reconc bootstrap inspect: " + err.Error()}
	}
	hookKinds := []string{}
	gitDirPresent := dirExists(filepath.Join(inspection.RepoRoot, ".git"))
	if gitDirPresent && !skipGitHook {
		hookKinds = append(hookKinds, hooks.KindGitPreCommit)
	}
	if !skipAgentHooks {
		hookKinds = append(hookKinds, inspection.DetectedPlatforms...)
	}
	plan, err := reconbootstrap.BuildPlan(reconbootstrap.Request{
		RepoRoot: inspection.RepoRoot, Profile: reconbootstrap.ProfileMinimal,
		Packs: opts.Presets, Hooks: hookKinds, TrustExistingWrapper: true,
	}, version)
	if err != nil {
		return &CLIError{ExitCode: 1, Message: "reconc bootstrap plan: " + err.Error()}
	}
	applyReport, err := reconbootstrap.Apply(plan, version)
	if err != nil {
		return &CLIError{ExitCode: 1, Message: "reconc bootstrap apply: " + err.Error()}
	}

	steps := []string{fmt.Sprintf("transaction %s: created=%v, unchanged=%v, candidates=%v",
		applyReport.Status, applyReport.Created, applyReport.Unchanged, applyReport.Candidates)}
	hints := []string{}
	if !gitDirPresent {
		hints = append(hints, "no .git/ found - run `git init` then `reconc hook install git-pre-commit` to enable commit-time enforcement")
	}
	for _, platform := range hooks.AgentPlatforms() {
		if containsString(inspection.DetectedPlatforms, platform.Kind) && !skipAgentHooks {
			steps = append(steps, "hook install "+platform.Kind+": transaction verified")
			continue
		}
		if !containsString(inspection.DetectedPlatforms, platform.Kind) {
			hints = append(hints, fmt.Sprintf("%s: create %s then `reconc hook install %s`", platform.DisplayName, strings.Join(platform.Activation.ConfigDirs, " or "), platform.Kind))
		}
	}

	hookStatuses, statusErr := hooks.InspectPlatforms(inspection.RepoRoot)
	installFailed := statusErr != nil
	if statusErr != nil {
		hints = append(hints, "Hook activation inspection failed: "+statusErr.Error())
	}
	healthy := !installFailed && applyReport.Status == reconbootstrap.ApplyComplete
	for _, report := range hookStatuses {
		if report.State != hooks.StateAbsent && report.State != hooks.StateConfigured {
			healthy = false
		}
	}

	if jsonOut {
		payload := map[string]interface{}{
			"repo_root":     inspection.RepoRoot,
			"steps":         steps,
			"hook_statuses": hookStatuses,
			"next_hints":    hints,
			"healthy":       healthy,
		}
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(payload); err != nil {
			return &CLIError{ExitCode: 1, Message: "reconc bootstrap: json encode: " + err.Error()}
		}
		if !healthy {
			return &CLIError{ExitCode: 1, Message: ""}
		}
		return nil
	}

	fmt.Fprintf(stdout, "Bootstrapped reconc at %s\n\n", inspection.RepoRoot)
	for i, s := range steps {
		fmt.Fprintf(stdout, "  %d. %s\n", i+1, s)
	}
	if len(hookStatuses) > 0 {
		fmt.Fprintln(stdout, "")
		fmt.Fprintln(stdout, "Hook status:")
		for _, report := range hookStatuses {
			fmt.Fprintf(stdout, "  - %s: %s (%s)\n", report.Kind, report.State, report.Detail)
		}
	}
	fmt.Fprintln(stdout, "")
	fmt.Fprintln(stdout, "Next steps:")
	for _, h := range hints {
		fmt.Fprintf(stdout, "  - %s\n", h)
	}
	if !healthy {
		return &CLIError{ExitCode: 1, Message: ""}
	}
	return nil
}
