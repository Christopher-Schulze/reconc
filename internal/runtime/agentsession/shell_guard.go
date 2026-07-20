package agentsession

import (
	"strings"

	"reconc.dev/reconc/internal/shellcommand"
)

func forbiddenShellCommandReason(command string) string {
	invocations, complete := shellcommand.Invocations(command, maxShellGuardDepth)
	if !complete {
		return "reconc blocked a shell command that could not be completely parsed within the bounded policy safety limits"
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
