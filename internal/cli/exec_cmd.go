package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"
	"unicode"

	"reconc.dev/reconc/internal/commandproof"
	"reconc.dev/reconc/internal/ingest"
	"reconc.dev/reconc/internal/runtime/agentsession"
)

func runExec(args []string, stdout, stderr io.Writer) error {
	repo := "."
	repoSet := false
	staged := false
	shell := false
	separator := -1
	for index, arg := range args {
		if arg == "--" {
			separator = index
			break
		}
		switch arg {
		case "--staged":
			staged = true
		case "--shell":
			shell = true
		case "-h", "--help":
			printExecHelp(stdout)
			return nil
		default:
			if strings.HasPrefix(arg, "-") {
				return &CLIError{ExitCode: 1, Message: fmt.Sprintf("reconc exec: unknown flag %q", arg)}
			}
			if repoSet {
				return &CLIError{ExitCode: 1, Message: "reconc exec: pass at most one repo before --"}
			}
			repo = arg
			repoSet = true
		}
	}
	if separator < 0 || separator == len(args)-1 {
		return &CLIError{ExitCode: 1, Message: "reconc exec: command required after --"}
	}
	commandArgs := args[separator+1:]
	if shell && len(commandArgs) != 1 {
		return &CLIError{ExitCode: 1, Message: "reconc exec: --shell requires one literal command argument after --"}
	}

	discovery, err := ingest.DiscoverPolicyRepo(repo)
	if err != nil {
		return &CLIError{ExitCode: 1, Message: "reconc exec: " + err.Error()}
	}
	if !discovery.Discovered {
		return &CLIError{ExitCode: 1, Message: "reconc exec: no policy markers found"}
	}

	commandText := renderDirectCommand(commandArgs)
	executionMode := "direct"
	var command *exec.Cmd
	if shell {
		commandText = strings.TrimSpace(commandArgs[0])
		if commandText == "" {
			return &CLIError{ExitCode: 1, Message: "reconc exec: shell command must be non-empty"}
		}
		executionMode = "shell"
		command = shellCommand(commandText)
	} else {
		command = exec.Command(commandArgs[0], commandArgs[1:]...)
	}

	var snapshot commandproof.Snapshot
	if staged {
		snapshot, err = commandproof.CaptureStagedClean(discovery.RepoRoot)
		if err != nil {
			return &CLIError{ExitCode: 1, Message: "reconc exec: staged precondition: " + err.Error()}
		}
	}
	command.Dir = discovery.RepoRoot
	command.Stdin = os.Stdin
	command.Stdout = stdout
	command.Stderr = stderr
	startedAt := time.Now().UTC()
	runErr := command.Run()
	completedAt := time.Now().UTC()
	exitCode := commandExitCode(runErr)
	outcome := "success"
	if runErr != nil {
		outcome = "failure"
	}
	var postconditionErr error
	if runErr == nil && staged {
		postconditionErr = commandproof.VerifyStagedClean(snapshot)
		if postconditionErr != nil {
			outcome = "failure"
		}
	}
	if recordErr := agentsession.RecordCommandOutcome(discovery.RepoRoot, commandText, outcome, exitCode); recordErr != nil {
		message := fmt.Sprintf("reconc exec: record active-session evidence: %v", recordErr)
		if runErr != nil {
			message += fmt.Sprintf("; command also failed with exit code %d", exitCode)
		}
		if postconditionErr != nil {
			message += "; staged postcondition also failed: " + postconditionErr.Error()
		}
		return &CLIError{ExitCode: 1, Message: message}
	}
	if runErr != nil {
		return &CLIError{ExitCode: exitCode, Message: fmt.Sprintf("reconc exec: command failed with exit code %d", exitCode)}
	}
	if postconditionErr != nil {
		return &CLIError{ExitCode: 1, Message: "reconc exec: staged postcondition: " + postconditionErr.Error()}
	}
	if staged {
		proof, err := commandproof.StoreSuccess(snapshot, commandText, executionMode, startedAt, completedAt)
		if err != nil {
			return &CLIError{ExitCode: 1, Message: "reconc exec: store command proof: " + err.Error()}
		}
		fmt.Fprintf(stderr, "reconc exec: verified staged index %s with proof %s\n", proof.IndexTree[:12], proof.Digest[:12])
	}
	return nil
}

func printExecHelp(stdout io.Writer) {
	fmt.Fprintln(stdout, "Usage: reconc exec [repo] [--staged] [--shell] -- COMMAND [ARG ...]")
	fmt.Fprintln(stdout, "")
	fmt.Fprintln(stdout, "Execute a command from the repo root and record its real exit status.")
	fmt.Fprintln(stdout, "  --staged  require a clean staged-only tree and bind success to HEAD + index")
	fmt.Fprintln(stdout, "  --shell   execute one literal command through the platform shell")
}

func commandExitCode(err error) int {
	if err == nil {
		return 0
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && exitErr.ExitCode() > 0 {
		return exitErr.ExitCode()
	}
	return 1
}

func renderDirectCommand(args []string) string {
	quoted := make([]string, len(args))
	for index, arg := range args {
		quoted[index] = quoteCommandArgument(arg)
	}
	return strings.Join(quoted, " ")
}

func quoteCommandArgument(argument string) string {
	if argument != "" && strings.IndexFunc(argument, func(char rune) bool {
		return !(unicode.IsLetter(char) || unicode.IsDigit(char) || strings.ContainsRune("_@%+=:,./-", char))
	}) < 0 {
		return argument
	}
	return "'" + strings.ReplaceAll(argument, "'", "'\"'\"'") + "'"
}
