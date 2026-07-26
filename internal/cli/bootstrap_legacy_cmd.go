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
	acceptManagedBlocks := false
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
		case "--accept-managed-blocks":
			acceptManagedBlocks = true
		case "--preset":
			val, ok := nextArgValue(args, &i, a)
			if !ok {
				return &CLIError{ExitCode: 1, Message: "reconc bootstrap: --preset requires a value"}
			}
			opts.Presets = append(opts.Presets, val)
		case "-h", "--help":
			fmt.Fprintln(stdout, "Usage: reconc bootstrap [repo] [--preset NAME ...]")
			fmt.Fprintln(stdout, "                       [--skip-git-hook] [--skip-agent-hooks]")
			fmt.Fprintln(stdout, "                       [--accept-managed-blocks] [--json]")
			fmt.Fprintln(stdout, "")
			fmt.Fprintln(stdout, "Compatibility bootstrap: create-only minimal transaction with detected hooks.")
			fmt.Fprintln(stdout, "- git pre-commit is installed when .git/ is present.")
			fmt.Fprintln(stdout, "- Registered agent hooks are installed when their repo-local config dirs exist.")
			fmt.Fprintln(stdout, "- --accept-managed-blocks explicitly promotes only byte-verified marker-only candidates.")
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
	if err := ensureCurrentUserCLI("compatibility"); err != nil {
		return err
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
		agentKinds := map[string]bool{}
		for _, platform := range hooks.AgentPlatforms() {
			agentKinds[platform.Kind] = true
		}
		for _, kind := range inspection.DetectedPlatforms {
			if agentKinds[kind] {
				hookKinds = append(hookKinds, kind)
			}
		}
	}
	request := reconbootstrap.Request{
		RepoRoot: inspection.RepoRoot, Profile: reconbootstrap.ProfileMinimal,
		Packs: opts.Presets, Hooks: hookKinds, TrustExistingWrapper: true,
	}
	plan, err := reconbootstrap.BuildPlan(request, version)
	if err != nil {
		return &CLIError{ExitCode: 1, Message: "reconc bootstrap plan: " + err.Error()}
	}
	applyReport, err := reconbootstrap.Apply(plan, version)
	if err != nil {
		return &CLIError{ExitCode: 1, Message: "reconc bootstrap apply: " + err.Error()}
	}
	acceptedDetails := []string{}
	if acceptManagedBlocks && applyReport.Status == reconbootstrap.ApplyDrift {
		accepted, acceptErr := reconbootstrap.AcceptManagedCandidates(plan, applyReport)
		if acceptErr != nil {
			return &CLIError{ExitCode: 1, Message: "reconc bootstrap accept managed blocks: " + acceptErr.Error()}
		}
		acceptedDetails = append(acceptedDetails,
			fmt.Sprintf("accepted marker-only updates=%v; removed candidates=%v", accepted.Updated, accepted.RemovedCandidates),
		)
		plan, err = reconbootstrap.BuildPlan(request, version)
		if err != nil {
			return &CLIError{ExitCode: 1, Message: "reconc bootstrap replan: " + err.Error()}
		}
		applyReport, err = reconbootstrap.Apply(plan, version)
		if err != nil {
			return &CLIError{ExitCode: 1, Message: "reconc bootstrap reapply: " + err.Error()}
		}
	}

	steps := []string{fmt.Sprintf("transaction %s: created=%v, unchanged=%v, candidates=%v",
		applyReport.Status, applyReport.Created, applyReport.Unchanged, applyReport.Candidates)}
	details := []string{}
	details = append(details, acceptedDetails...)
	if !gitDirPresent {
		details = append(details, "no .git/ found; commit-time enforcement is not installed")
	}
	for _, ambiguity := range inspection.Ambiguities {
		details = append(details, "ambiguous detection: "+ambiguity)
	}
	for _, kind := range hookKinds {
		steps = append(steps, "hook install "+kind+": transaction verified")
	}

	allHookStatuses, statusErr := hooks.InspectPlatforms(inspection.RepoRoot)
	installFailed := statusErr != nil
	if statusErr != nil {
		details = append(details, "hook activation inspection failed: "+statusErr.Error())
	}
	selectedKinds := map[string]bool{}
	for _, kind := range hookKinds {
		selectedKinds[kind] = true
	}
	hookStatuses := []hooks.PlatformStatus{}
	for _, status := range allHookStatuses {
		if selectedKinds[status.Kind] || status.State != hooks.StateAbsent {
			hookStatuses = append(hookStatuses, status)
		}
	}
	healthy := !installFailed && applyReport.Status == reconbootstrap.ApplyComplete
	primaryNext := renderDirectCommand([]string{"reconc", "check", inspection.RepoRoot})
	for _, report := range hookStatuses {
		if report.State != hooks.StateAbsent && report.State != hooks.StateConfigured {
			healthy = false
			if report.Remediation != "" && primaryNext == renderDirectCommand([]string{"reconc", "check", inspection.RepoRoot}) {
				primaryNext = strings.TrimSuffix(strings.TrimPrefix(report.Remediation, "Run `"), "`.")
			}
		}
	}
	if !gitDirPresent && applyReport.Status == reconbootstrap.ApplyComplete {
		primaryNext = "git init && " + renderDirectCommand([]string{"reconc", "hook", "install", hooks.KindGitPreCommit, inspection.RepoRoot})
	}
	if applyReport.Status == reconbootstrap.ApplyDrift && len(applyReport.Candidates) > 0 {
		if reconbootstrap.HasManagedCandidates(plan) {
			primaryNext = renderDirectCommand([]string{"reconc", "bootstrap", inspection.RepoRoot, "--accept-managed-blocks"})
		} else {
			candidate := applyReport.Candidates[0]
			primaryNext = "review " + candidate + ", integrate the approved change, remove the candidate, then rerun " + renderDirectCommand([]string{"reconc", "bootstrap", inspection.RepoRoot})
		}
		for _, extra := range applyReport.Candidates[1:] {
			details = append(details, "additional removal candidate: "+extra)
		}
	}

	if jsonOut {
		payload := map[string]interface{}{
			"repo_root":     inspection.RepoRoot,
			"steps":         steps,
			"hook_statuses": hookStatuses,
			"details":       details,
			"next_action":   primaryNext,
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
	if len(details) > 0 {
		fmt.Fprintln(stdout, "Details:")
		for _, detail := range details {
			fmt.Fprintf(stdout, "  - %s\n", detail)
		}
	}
	fmt.Fprintf(stdout, "Next: %s\n", primaryNext)
	if !healthy {
		return &CLIError{ExitCode: 1, Message: ""}
	}
	return nil
}
