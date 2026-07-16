package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"reconc.dev/reconc/internal/grokacp"
)

func runGrok(args []string, stdout, stderr io.Writer) error {
	repo := "."
	repoSet := false
	prompt := ""
	model := ""
	grokBinary := "grok"
	maxContinuations := 0

	for index := 0; index < len(args); index++ {
		arg := args[index]
		switch arg {
		case "-h", "--help":
			printGrokHelp(stdout)
			return nil
		case "--prompt":
			value, ok := nextArgValue(args, &index, arg)
			if !ok {
				return &CLIError{ExitCode: 1, Message: "reconc grok: --prompt requires text"}
			}
			if prompt != "" {
				return &CLIError{ExitCode: 1, Message: "reconc grok: prompt was provided more than once"}
			}
			prompt = value
		case "--model":
			value, ok := nextArgValue(args, &index, arg)
			if !ok {
				return &CLIError{ExitCode: 1, Message: "reconc grok: --model requires an ID"}
			}
			model = value
		case "--grok-binary":
			value, ok := nextArgValue(args, &index, arg)
			if !ok {
				return &CLIError{ExitCode: 1, Message: "reconc grok: --grok-binary requires a path"}
			}
			grokBinary = value
		case "--max-continuations":
			value, ok := nextArgValue(args, &index, arg)
			if !ok {
				return &CLIError{ExitCode: 1, Message: "reconc grok: --max-continuations requires an integer"}
			}
			parsed, err := atoi(value)
			if err != nil || parsed < 1 {
				return &CLIError{ExitCode: 1, Message: "reconc grok: --max-continuations must be at least 1"}
			}
			maxContinuations = parsed
		case "--":
			if prompt != "" {
				return &CLIError{ExitCode: 1, Message: "reconc grok: use either --prompt or trailing text after --, not both"}
			}
			prompt = strings.Join(args[index+1:], " ")
			index = len(args)
		default:
			if strings.HasPrefix(arg, "-") {
				return &CLIError{ExitCode: 1, Message: fmt.Sprintf("reconc grok: unknown flag %q", arg)}
			}
			if repoSet {
				return &CLIError{ExitCode: 1, Message: "reconc grok: expected at most one repo path before --"}
			}
			repo = arg
			repoSet = true
		}
	}
	if strings.TrimSpace(prompt) == "" {
		return &CLIError{ExitCode: 1, Message: "reconc grok: missing prompt; use --prompt TEXT or `-- TEXT`"}
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	err := grokacp.Run(ctx, grokacp.Options{
		RepoRoot:         repo,
		GrokBinary:       grokBinary,
		Model:            model,
		Prompt:           prompt,
		MaxContinuations: maxContinuations,
		Stdout:           stdout,
		Stderr:           stderr,
	})
	if err == nil {
		return nil
	}
	var blocked *grokacp.PolicyBlockedError
	if errors.As(err, &blocked) {
		return &CLIError{ExitCode: 2, Message: "reconc grok: " + blocked.Error()}
	}
	return &CLIError{ExitCode: 1, Message: "reconc grok: " + err.Error()}
}

func printGrokHelp(stdout io.Writer) {
	fmt.Fprintln(stdout, "Usage: reconc grok [repo] [--model ID] [--grok-binary PATH] [--max-continuations N] --prompt TEXT")
	fmt.Fprintln(stdout, "       reconc grok [repo] [flags] -- TEXT")
	fmt.Fprintln(stdout, "")
	fmt.Fprintln(stdout, "Runs the unmodified official `grok agent stdio` runtime with the native")
	fmt.Fprintln(stdout, "Reconc Grok hook installed. The same ACP session is re-prompted until")
	fmt.Fprintln(stdout, "Reconc's strict Stop gate is clean. Ctrl-C stops the driver immediately.")
	fmt.Fprintln(stdout, "")
	fmt.Fprintln(stdout, "The ACP client uses Grok's always-approve transport because it has no TUI")
	fmt.Fprintln(stdout, "permission modal; Reconc PreToolUse and Grok's explicit deny rules still run.")
}
