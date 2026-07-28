package cli

import (
	"fmt"
	"io"
	"strings"

	reconbootstrap "reconc.dev/reconc/internal/bootstrap"
)

func runBootstrapLegacy(args []string, version string, stdout, stderr io.Writer) error {
	options := initCLIOptions{request: reconbootstrap.InitRequest{
		RepoRoot: ".",
		CompatibilityWarning: []string{
			"reconc bootstrap [repo] is a compatibility alias; use reconc init [repo]",
		},
	}}
	repoSet := false
	for index := 0; index < len(args); index++ {
		arg := args[index]
		switch arg {
		case "--json":
			options.jsonOut = true
		case "--force":
			return bootstrapCLIError("compatibility", "--force is unsupported; use reconc init and review candidates")
		case "--skip-git-hook":
			options.request.SkipGitHook = true
		case "--skip-agent-hooks":
			options.request.SkipAgentHooks = true
		case "--accept-managed-blocks":
			options.request.AcceptManagedBlocks = true
		case "--preset":
			value, ok := nextArgValue(args, &index, arg)
			if !ok {
				return &CLIError{ExitCode: 1, Message: "reconc bootstrap: --preset requires a value"}
			}
			options.request.Packs = append(options.request.Packs, value)
			options.request.CompatibilityWarning = append(options.request.CompatibilityWarning,
				"--preset is deprecated and was mapped to --pack; use reconc init --pack")
		case "-h", "--help":
			fmt.Fprintln(stdout, "Usage: reconc bootstrap [repo] [--preset NAME ...]")
			fmt.Fprintln(stdout, "                       [--skip-git-hook] [--skip-agent-hooks]")
			fmt.Fprintln(stdout, "                       [--accept-managed-blocks] [--json]")
			fmt.Fprintln(stdout, "")
			fmt.Fprintln(stdout, "Compatibility alias for reconc init. It uses the same transaction engine.")
			return nil
		default:
			if strings.HasPrefix(arg, "-") {
				return &CLIError{ExitCode: 1, Message: fmt.Sprintf("reconc bootstrap: unknown flag %q", arg)}
			}
			if repoSet {
				return &CLIError{ExitCode: 1, Message: fmt.Sprintf("reconc bootstrap: unexpected argument %q", arg)}
			}
			options.request.RepoRoot = arg
			repoSet = true
		}
	}
	return runInitOperation(options, version, stdout, stderr)
}
