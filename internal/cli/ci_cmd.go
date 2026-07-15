package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"reconc.dev/reconc/internal/ingest"
	"reconc.dev/reconc/internal/runtime"
	"reconc.dev/reconc/internal/runtime/agentsession"
	"time"
)

// runCI implements `reconc ci [repo] (--staged | --base REF [--head REF])
// [--read PATH ...] [--command CMD ...] [--claim NAME ...] [--json]`.
//
// Derives write_paths from a git diff (staged OR base..head range),
// merges with explicit --read/--command/--claim flags, and runs check.
// Exit codes: 0 = pass/warn, 1 = error, 2 = blocking violation.
//
// The most common shapes:
//   - reconc ci --staged                  (used by git pre-commit hook)
//   - reconc ci --base main               (used by PR / CI pipelines)
//   - reconc ci --base main --head HEAD   (explicit range)
func runCI(args []string, stdout, stderr io.Writer) error {
	repo := "."
	jsonOut := false
	staged := false
	outputPath := ""
	base := ""
	head := ""
	inputs := runtime.Empty()

	i := 0
	for i < len(args) {
		a := args[i]
		switch a {
		case "--json":
			jsonOut = true
		case "--staged":
			staged = true
		case "--output":
			val, ok := nextArgValue(args, &i, a)
			if !ok {
				return &CLIError{ExitCode: 1, Message: "reconc ci: --output requires a path"}
			}
			outputPath = val
		case "--base":
			val, ok := nextArgValue(args, &i, a)
			if !ok {
				return &CLIError{ExitCode: 1, Message: "reconc ci: --base requires a ref"}
			}
			base = val
		case "--head":
			val, ok := nextArgValue(args, &i, a)
			if !ok {
				return &CLIError{ExitCode: 1, Message: "reconc ci: --head requires a ref"}
			}
			head = val
		case "--read":
			val, ok := nextArgValue(args, &i, a)
			if !ok {
				return &CLIError{ExitCode: 1, Message: "reconc ci: --read requires a value"}
			}
			inputs.ReadPaths = append(inputs.ReadPaths, val)
		case "--command":
			val, ok := nextArgValue(args, &i, a)
			if !ok {
				return &CLIError{ExitCode: 1, Message: "reconc ci: --command requires a value"}
			}
			inputs.Commands = append(inputs.Commands, val)
		case "--command-success":
			val, ok := nextArgValue(args, &i, a)
			if !ok {
				return &CLIError{ExitCode: 1, Message: "reconc ci: --command-success requires a value"}
			}
			inputs.Commands = append(inputs.Commands, val)
			inputs.CommandResults = append(inputs.CommandResults, runtime.CommandResult{
				Command: val, Outcome: runtime.CommandOutcomeSuccess, EvidenceEpoch: runtime.ExplicitEvidenceEpoch,
			})
		case "--command-failure":
			val, ok := nextArgValue(args, &i, a)
			if !ok {
				return &CLIError{ExitCode: 1, Message: "reconc ci: --command-failure requires a value"}
			}
			inputs.Commands = append(inputs.Commands, val)
			inputs.CommandResults = append(inputs.CommandResults, runtime.CommandResult{
				Command: val, Outcome: runtime.CommandOutcomeFailure, EvidenceEpoch: runtime.ExplicitEvidenceEpoch,
			})
		case "--claim":
			val, ok := nextArgValue(args, &i, a)
			if !ok {
				return &CLIError{ExitCode: 1, Message: "reconc ci: --claim requires a value"}
			}
			inputs.Claims = append(inputs.Claims, val)
		case "--auto-claim":
			if detectCIEnvironment() {
				inputs.Claims = append(inputs.Claims, "ci-green")
			}
		case "-h", "--help":
			fmt.Fprintln(stdout, "Usage: reconc ci [repo] (--staged | --base REF [--head REF])")
			fmt.Fprintln(stdout, "                 [--read PATH] [--command CMD] [--command-success CMD]")
			fmt.Fprintln(stdout, "                 [--command-failure CMD] [--claim NAME] [--auto-claim] [--json] [--output PATH]")
			fmt.Fprintln(stdout, "")
			fmt.Fprintln(stdout, "Derive write_paths from git diff and run policy check.")
			fmt.Fprintln(stdout, "  --staged              git diff --cached --name-only (pre-commit)")
			fmt.Fprintln(stdout, "  --base REF [--head REF]  git diff base...head --name-only (PR/CI)")
			fmt.Fprintln(stdout, "Exit codes: 0 = pass/warn, 1 = error, 2 = blocking violation.")
			return nil
		default:
			if len(a) > 0 && a[0] == '-' {
				return &CLIError{ExitCode: 1, Message: fmt.Sprintf("reconc ci: unknown flag %q", a)}
			}
			repo = a
		}
		i++
	}

	// Need to discover the repo to know what dir to run git from.
	discovery, err := ingest.DiscoverPolicyRepo(repo)
	if err != nil {
		return &CLIError{ExitCode: 1, Message: "reconc ci: " + err.Error()}
	}
	if !discovery.Discovered {
		return &CLIError{ExitCode: 1, Message: "reconc ci: no policy markers found"}
	}
	activeEvidence, activeEvidenceErr := agentsession.ActiveEvidence(discovery.RepoRoot)
	if activeEvidenceErr == nil {
		inputs.ReadPaths = append(inputs.ReadPaths, activeEvidence.ReadPaths...)
		inputs.Commands = append(inputs.Commands, activeEvidence.Commands...)
		for _, result := range activeEvidence.CommandResults {
			inputs.CommandResults = append(inputs.CommandResults, runtime.CommandResult{
				Command:       result.Command,
				Outcome:       result.Outcome,
				EvidenceEpoch: result.EvidenceEpoch,
			})
		}
		inputs.Claims = append(inputs.Claims, activeEvidence.Claims...)
	}

	gitPaths, gitMeta, err := runtime.CollectGitWritePaths(discovery.RepoRoot, staged, base, head)
	if err != nil {
		return &CLIError{ExitCode: 1, Message: "reconc ci: " + err.Error()}
	}
	inputs.WritePaths = append(inputs.WritePaths, gitPaths...)
	if inputs.WriteEpochs == nil {
		inputs.WriteEpochs = map[string]uint64{}
	}
	gitEpoch := activeEvidence.EvidenceEpoch
	if gitEpoch < runtime.ExplicitEvidenceEpoch-1 {
		gitEpoch++
	}
	for _, path := range gitPaths {
		epoch := activeEvidence.WriteEpochs[path]
		if epoch == 0 {
			epoch = gitEpoch
		}
		inputs.WriteEpochs[path] = epoch
	}

	startCI := time.Now()
	report, err := runtime.CheckRepoPolicy(repo, inputs)
	if err != nil {
		return &CLIError{ExitCode: 1, Message: "reconc ci: " + err.Error()}
	}
	maybeAudit("ci", report, startCI)
	out, closeOutput, err := teeToFile(stdout, outputPath)
	if err != nil {
		return &CLIError{ExitCode: 1, Message: "reconc ci: open output file: " + err.Error()}
	}
	defer func() { _ = closeOutput() }()

	if jsonOut {
		// Embed git metadata into the JSON output for auditability.
		payload := map[string]interface{}{
			"report": report,
			"git":    gitMeta,
		}
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		if err := enc.Encode(payload); err != nil {
			return &CLIError{ExitCode: 1, Message: "reconc ci: json encode: " + err.Error()}
		}
	} else {
		fmt.Fprintf(out, "Git:       %s (%d path(s))\n", gitMeta.GitCommand, gitMeta.WritePathCount)
		renderCheckText(report, out)
	}

	if report.Decision == runtime.DecisionBlock {
		return &CLIError{ExitCode: 2, Message: ""}
	}
	return nil
}
