package agentsession

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"
	"time"

	"reconc.dev/reconc/internal/gitexec"
	"reconc.dev/reconc/internal/shellcommand"
)

func forbiddenShellCommandReason(command string) string {
	return forbiddenShellCommandReasonInRepo("", command)
}

func forbiddenShellCommandReasonInRepo(repoRoot, command string) string {
	return forbiddenShellCommandReasonWithAliases(repoRoot, command, make(map[string]gitAlias), 0)
}

func forbiddenShellCommandReasonInRepoWithAliasSnapshot(repoRoot, command string, snapshot gitAliasSnapshot) string {
	return forbiddenShellCommandReasonWithAliasState(repoRoot, command, snapshot.workingAliases(), snapshot.complete, 0)
}

type gitAlias struct {
	value   string
	dynamic bool
}

func forbiddenShellCommandReasonWithAliases(repoRoot, command string, aliases map[string]gitAlias, aliasDepth int) string {
	return forbiddenShellCommandReasonWithAliasState(repoRoot, command, aliases, false, aliasDepth)
}

func forbiddenShellCommandReasonWithAliasState(repoRoot, command string, aliases map[string]gitAlias, aliasesComplete bool, aliasDepth int) string {
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
		if wordIsDynamic(invocation, subcommandIndex) {
			return "reconc blocked a git command whose subcommand is dynamic, so destructive aliases or built-ins cannot be excluded before execution. Write the git subcommand as a literal word."
		}
		inlineAliases, unknownDynamicAlias := gitInlineAliases(invocation, subcommandIndex)
		invocationAliases := aliases
		if len(inlineAliases) > 0 {
			invocationAliases = cloneGitAliases(aliases)
			for name, alias := range inlineAliases {
				invocationAliases[name] = alias
			}
		}
		if reason := forbiddenGitInvocationReason(repoRoot, invocation, subcommandIndex, invocationAliases, aliasesComplete, unknownDynamicAlias, aliasDepth); reason != "" {
			return reason
		}
	}
	return ""
}

func forbiddenGitInvocationReason(repoRoot string, invocation shellcommand.Invocation, subcommandIndex int, aliases map[string]gitAlias, aliasesComplete, unknownDynamicAlias bool, aliasDepth int) string {
	subcommand := strings.ToLower(invocation.Words[subcommandIndex])
	args := invocation.Words[subcommandIndex+1:]
	dynamicArgs := invocation.DynamicWords[subcommandIndex+1:]
	switch subcommand {
	case "clean":
		if !hasGitCleanDryRun(args) {
			return "reconc blocked destructive git clean before execution. Use a dry-run (`git clean -nd`) and get explicit user approval before deleting untracked files."
		}
	case "reset":
		if hasArg(args, "--hard") || anyDynamicWord(dynamicArgs) {
			return "reconc blocked git reset --hard or dynamically configured git reset before execution. Preserve user work and use literal non-destructive reset arguments, or ask for explicit approval before hard-resetting tracked changes."
		}
	case "config":
		recordGitConfigAlias(invocation, subcommandIndex, aliases)
		return ""
	}
	if knownGitCommand(subcommand) {
		return ""
	}
	if unknownDynamicAlias {
		return fmt.Sprintf("reconc blocked git subcommand %q because a dynamic inline `alias.*` configuration could change what it executes. Use a literal alias name and value.", subcommand)
	}
	alias, found := aliases[subcommand]
	if !found && repoRoot != "" && !aliasesComplete {
		var err error
		alias.value, found, err = configuredGitAlias(repoRoot, subcommand)
		if err != nil {
			return fmt.Sprintf("reconc blocked git subcommand %q because its alias could not be inspected safely: %v", subcommand, err)
		}
	}
	if !found {
		return fmt.Sprintf("reconc blocked unknown git subcommand %q because it may resolve to an uninspected alias or executable. Use a built-in git command or define a literal alias that reconc can analyze.", subcommand)
	}
	if alias.dynamic {
		return fmt.Sprintf("reconc blocked git alias %q because its value is dynamic and cannot be inspected before execution. Define the alias with a literal value.", subcommand)
	}
	if aliasDepth >= maxGitAliasDepth {
		return fmt.Sprintf("reconc blocked git alias %q because expansion exceeded %d levels. Flatten the alias chain.", subcommand, maxGitAliasDepth)
	}
	return forbiddenGitAliasReason(repoRoot, subcommand, alias.value, args, dynamicArgs, aliases, aliasesComplete, aliasDepth+1)
}

func forbiddenGitAliasReason(repoRoot, name, value string, args []string, dynamicArgs []bool, aliases map[string]gitAlias, aliasesComplete bool, aliasDepth int) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return fmt.Sprintf("reconc blocked git alias %q because its value is empty and cannot be inspected safely.", name)
	}
	command := value
	if strings.HasPrefix(command, "!") {
		command = strings.TrimSpace(strings.TrimPrefix(command, "!"))
		command = appendGitAliasArgs(command, args, dynamicArgs)
	} else {
		command = "git " + command
		command = appendGitAliasArgs(command, args, dynamicArgs)
	}
	return forbiddenShellCommandReasonWithAliasState(repoRoot, command, aliases, aliasesComplete, aliasDepth)
}

func appendGitAliasArgs(command string, args []string, dynamicArgs []bool) string {
	for index, arg := range args {
		if index < len(dynamicArgs) && dynamicArgs[index] {
			command += " " + arg
			continue
		}
		command += " " + shellQuote(arg)
	}
	return command
}

func gitInlineAliases(invocation shellcommand.Invocation, subcommandIndex int) (map[string]gitAlias, bool) {
	aliases := make(map[string]gitAlias)
	unknownDynamicAlias := false
	for index := 1; index < subcommandIndex; index++ {
		token := invocation.Words[index]
		dynamic := wordIsDynamic(invocation, index)
		var setting string
		switch {
		case token == "-c" && index+1 < subcommandIndex:
			index++
			setting = invocation.Words[index]
			dynamic = dynamic || wordIsDynamic(invocation, index)
		case strings.HasPrefix(token, "-c") && len(token) > 2:
			setting = strings.TrimPrefix(token, "-c")
		default:
			continue
		}
		name, value, found := parseGitAliasSetting(setting)
		if !found {
			if dynamic && strings.Contains(strings.ToLower(setting), "alias.") {
				unknownDynamicAlias = true
			}
			continue
		}
		aliases[name] = gitAlias{value: value, dynamic: dynamic}
	}
	return aliases, unknownDynamicAlias
}

func cloneGitAliases(source map[string]gitAlias) map[string]gitAlias {
	cloned := make(map[string]gitAlias, len(source))
	for name, alias := range source {
		cloned[name] = alias
	}
	return cloned
}

func parseGitAliasSetting(setting string) (name, value string, found bool) {
	key, value, found := strings.Cut(setting, "=")
	if !found {
		return "", "", false
	}
	name, found = parseGitAliasName(key)
	if !found {
		return "", "", false
	}
	return name, value, true
}

func parseGitAliasName(key string) (string, bool) {
	lower := strings.ToLower(key)
	if !strings.HasPrefix(lower, "alias.") {
		return "", false
	}
	name := strings.TrimPrefix(lower, "alias.")
	if name == "" {
		return "", false
	}
	return name, true
}

func recordGitConfigAlias(invocation shellcommand.Invocation, subcommandIndex int, aliases map[string]gitAlias) {
	args := invocation.Words[subcommandIndex+1:]
	if gitConfigReadsValue(args) {
		return
	}
	for index, arg := range args {
		lower := strings.ToLower(arg)
		if !strings.HasPrefix(lower, "alias.") || index+1 >= len(args) {
			continue
		}
		name := strings.TrimPrefix(lower, "alias.")
		if name == "" {
			return
		}
		if gitConfigRemovesValue(args[:index]) {
			delete(aliases, name)
			return
		}
		aliases[name] = gitAlias{
			value:   args[index+1],
			dynamic: wordIsDynamic(invocation, subcommandIndex+2+index),
		}
		return
	}
}

func gitConfigReadsValue(args []string) bool {
	for _, arg := range args {
		switch arg {
		case "--get", "--get-all", "--get-regexp", "--get-urlmatch", "--list", "-l":
			return true
		}
	}
	return false
}

func gitConfigRemovesValue(args []string) bool {
	for _, arg := range args {
		switch arg {
		case "--unset", "--unset-all", "--remove-section", "--rename-section":
			return true
		}
	}
	return false
}

func configuredGitAlias(repoRoot, name string) (string, bool, error) {
	output, exitCode, err := runGitInspection(repoRoot, "config", "--get", "alias."+name)
	if err != nil {
		return "", false, err
	}
	if exitCode == 1 {
		return "", false, nil
	}
	if exitCode != 0 {
		return "", false, fmt.Errorf("git config exited %d", exitCode)
	}
	return strings.TrimSuffix(strings.TrimSuffix(string(output), "\n"), "\r"), true, nil
}

const (
	maxGitAliasDepth      = 8
	maxGitInspectionBytes = 64 << 10
	gitInspectionTimeout  = 2 * time.Second
)

var (
	knownGitCommandsOnce sync.Once
	knownGitCommandsSet  map[string]struct{}
)

func knownGitCommand(name string) bool {
	knownGitCommandsOnce.Do(func() {
		knownGitCommandsSet = fallbackGitCommands()
		output, exitCode, err := runGitInspection("", "--list-cmds=main,others,nohelpers")
		if err != nil || exitCode != 0 {
			return
		}
		for _, command := range strings.Fields(string(output)) {
			knownGitCommandsSet[strings.ToLower(command)] = struct{}{}
		}
	})
	_, found := knownGitCommandsSet[name]
	return found
}

func fallbackGitCommands() map[string]struct{} {
	commands := []string{
		"add", "am", "apply", "archive", "bisect", "blame", "branch", "bundle", "cat-file", "check-attr", "check-ignore", "checkout", "cherry", "cherry-pick", "clean", "clone", "commit", "config", "describe", "diff", "difftool", "fetch", "format-patch", "fsck", "gc", "grep", "hash-object", "help", "init", "log", "ls-files", "ls-remote", "ls-tree", "merge", "mergetool", "mv", "notes", "pull", "push", "rebase", "reflog", "remote", "reset", "restore", "revert", "rev-list", "rev-parse", "rm", "show", "show-ref", "sparse-checkout", "stash", "status", "submodule", "switch", "tag", "worktree",
	}
	result := make(map[string]struct{}, len(commands))
	for _, command := range commands {
		result[command] = struct{}{}
	}
	return result
}

func runGitInspection(repoRoot string, args ...string) ([]byte, int, error) {
	ctx, cancel := context.WithTimeout(context.Background(), gitInspectionTimeout)
	defer cancel()
	command := gitexec.CommandContext(ctx, repoRoot, nil, args...)
	stdout, err := command.StdoutPipe()
	if err != nil {
		return nil, -1, fmt.Errorf("open git stdout: %w", err)
	}
	if err := command.Start(); err != nil {
		return nil, -1, fmt.Errorf("start git inspection: %w", err)
	}
	output, readErr := io.ReadAll(io.LimitReader(stdout, maxGitInspectionBytes+1))
	waitErr := command.Wait()
	if readErr != nil {
		return nil, -1, fmt.Errorf("read git inspection: %w", readErr)
	}
	if len(output) > maxGitInspectionBytes {
		return nil, -1, fmt.Errorf("git inspection exceeded %d bytes", maxGitInspectionBytes)
	}
	if ctx.Err() != nil {
		return nil, -1, fmt.Errorf("git inspection timed out: %w", ctx.Err())
	}
	if waitErr == nil {
		return output, 0, nil
	}
	var exitError *exec.ExitError
	if errors.As(waitErr, &exitError) {
		return output, exitError.ExitCode(), nil
	}
	return nil, -1, fmt.Errorf("wait for git inspection: %w", waitErr)
}

func wordIsDynamic(invocation shellcommand.Invocation, index int) bool {
	return index >= 0 && index < len(invocation.DynamicWords) && invocation.DynamicWords[index]
}

func anyDynamicWord(words []bool) bool {
	for _, dynamic := range words {
		if dynamic {
			return true
		}
	}
	return false
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'\''`) + "'"
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
