package agentsession

import (
	"fmt"
	"strings"

	"reconc.dev/reconc/internal/shellcommand"
)

func forbiddenShellCommandReason(command string) string {
	invocations, incomplete := shellcommand.InvocationsWithReason(command, maxShellGuardDepth)
	if incomplete != shellcommand.IncompleteNone {
		return unanalyzableShellCommandReason(incomplete)
	}
	for _, invocation := range invocations {
		tokens := invocation.Words
		if len(tokens) == 0 || !isGitToken(tokens[0]) {
			continue
		}
		subcommandIndex := gitSubcommandIndex(tokens, 1)
		if subcommandIndex < 0 {
			continue
		}
		subcommand := tokens[subcommandIndex]
		args := tokens[subcommandIndex+1:]
		switch subcommand {
		case "clean":
			if !hasGitCleanDryRun(args) {
				return "reconc blocked destructive git clean before execution. Use a dry-run (`git clean -nd`) and get explicit user approval before deleting untracked files."
			}
		case "reset":
			if hasArg(args, "--hard") {
				return "reconc blocked destructive git reset --hard before execution. Preserve user work and ask for explicit approval before hard-resetting tracked changes."
			}
		}
	}
	return ""
}

// unanalyzableShellCommandReason turns a fail-closed analysis outcome into a
// single-line block message that names the cause and the concrete fix. The
// guard cannot enforce policy on a command whose executables it cannot
// identify, so every branch here blocks; only the remediation differs.
func unanalyzableShellCommandReason(incomplete shellcommand.IncompleteReason) string {
	switch incomplete {
	case shellcommand.IncompleteDynamicCommand:
		return "reconc blocked a shell command whose executable comes from an expansion or substitution, so the binary that would run cannot be identified before it runs. Write the command name as a literal word (`git status`, not `$TOOL status`); dynamic arguments are fine."
	case shellcommand.IncompleteNestingDepth:
		return fmt.Sprintf("reconc blocked a shell command nested deeper than %d levels of subshells, substitutions, or `sh -c` bodies. Flatten it or run the inner commands as separate calls.", maxShellGuardDepth)
	case shellcommand.IncompleteTooLarge:
		return "reconc blocked a shell command larger than the analysis limit. Split it into separate calls, or move the body into a script file and run that file."
	case shellcommand.IncompleteUnparsable:
		return "reconc blocked a shell command that is not valid Bash syntax, so its executables cannot be determined. Fix the syntax (unbalanced quotes, parentheses, or heredocs) and retry."
	default:
		return "reconc blocked a shell command because bounded analysis could not enumerate every executable it may run. Simplify the command into separate calls with literal command names."
	}
}

const maxShellGuardDepth = 16

func isGitToken(token string) bool {
	token = strings.TrimSpace(token)
	return token == "git" || strings.HasSuffix(token, "/git")
}

func gitSubcommandIndex(tokens []string, start int) int {
	for i := start; i < len(tokens); i++ {
		token := tokens[i]
		if token == "-C" || token == "-c" || token == "--git-dir" || token == "--work-tree" || token == "--namespace" {
			i++
			continue
		}
		if strings.HasPrefix(token, "-C") && token != "-C" {
			continue
		}
		if strings.HasPrefix(token, "--git-dir=") || strings.HasPrefix(token, "--work-tree=") || strings.HasPrefix(token, "--namespace=") {
			continue
		}
		if strings.HasPrefix(token, "-") {
			continue
		}
		return i
	}
	return -1
}

func hasGitCleanDryRun(args []string) bool {
	for _, arg := range args {
		if arg == "--dry-run" {
			return true
		}
		if strings.HasPrefix(arg, "-") && !strings.HasPrefix(arg, "--") && strings.Contains(arg, "n") {
			return true
		}
	}
	return false
}

func hasArg(args []string, want string) bool {
	for _, arg := range args {
		if arg == want {
			return true
		}
	}
	return false
}
