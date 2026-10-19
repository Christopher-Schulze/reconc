package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"reconc.dev/reconc/internal/runtime/agentsession"
)

const hookPauseUsage = "Usage: reconc hook pause [repo] --for DURATION --reason TEXT [--json]"

// runHookPause is the operator's sanctioned valve: blocking hook events pass
// for a bounded time instead of hooks being removed or stubbed. Agent tool
// calls cannot reach it; the pre-tool guard refuses them before execution.
func runHookPause(args []string, stdout, stderr io.Writer) error {
	repo, jsonOut, values, err := parseHookAdminArgs("pause", hookPauseUsage, args, stdout, "--for", "--reason")
	if err != nil || values == nil {
		return err
	}
	if values["--for"] == "" || values["--reason"] == "" {
		return &CLIError{ExitCode: 1, Message: "reconc hook pause: --for DURATION and --reason TEXT are required"}
	}
	duration, err := time.ParseDuration(values["--for"])
	if err != nil {
		return &CLIError{ExitCode: 1, Message: fmt.Sprintf("reconc hook pause: invalid --for %q: %v", values["--for"], err)}
	}
	if err := confirmOperatorPresence("reconc hook pause", stderr); err != nil {
		return err
	}
	status, err := agentsession.PauseHooks(repo, duration, values["--reason"], time.Now())
	if err != nil {
		return &CLIError{ExitCode: 1, Message: "reconc hook pause: " + err.Error()}
	}
	return writeHookPauseStatus(stdout, status, jsonOut)
}

func runHookResume(args []string, stdout, stderr io.Writer) error {
	repo, jsonOut, values, err := parseHookAdminArgs("resume", "Usage: reconc hook resume [repo] [--json]", args, stdout)
	if err != nil || values == nil {
		return err
	}
	if err := confirmOperatorPresence("reconc hook resume", stderr); err != nil {
		return err
	}
	status, err := agentsession.ResumeHooks(repo, time.Now())
	if err != nil {
		return &CLIError{ExitCode: 1, Message: "reconc hook resume: " + err.Error()}
	}
	return writeHookPauseStatus(stdout, status, jsonOut)
}

func runHookPauseStatus(args []string, stdout io.Writer) error {
	repo, jsonOut, values, err := parseHookAdminArgs("pause-status", "Usage: reconc hook pause-status [repo] [--json]", args, stdout)
	if err != nil || values == nil {
		return err
	}
	status, err := agentsession.ReadHookPauseStatus(repo, time.Now())
	if err != nil {
		return &CLIError{ExitCode: 1, Message: "reconc hook pause-status: " + err.Error()}
	}
	return writeHookPauseStatus(stdout, status, jsonOut)
}

// parseHookAdminArgs accepts one optional repo operand, --json, and the named
// value flags. A nil map with a nil error means help was printed.
func parseHookAdminArgs(name, usage string, args []string, stdout io.Writer, valueFlags ...string) (string, bool, map[string]string, error) {
	repo := "."
	repoSet := false
	jsonOut := false
	values := map[string]string{}
	allowed := map[string]bool{}
	for _, flag := range valueFlags {
		allowed[flag] = true
	}
	for index := 0; index < len(args); index++ {
		arg := args[index]
		switch {
		case arg == "--json":
			jsonOut = true
		case arg == "-h" || arg == "--help":
			fmt.Fprintln(stdout, usage)
			return "", false, nil, nil
		case allowed[arg]:
			if index+1 >= len(args) {
				return "", false, nil, &CLIError{ExitCode: 1, Message: fmt.Sprintf("reconc hook %s: %s requires a value", name, arg)}
			}
			index++
			values[arg] = args[index]
		case strings.HasPrefix(arg, "-"):
			return "", false, nil, &CLIError{ExitCode: 1, Message: fmt.Sprintf("reconc hook %s: unknown flag %q", name, arg)}
		case repoSet:
			return "", false, nil, &CLIError{ExitCode: 1, Message: fmt.Sprintf("reconc hook %s: accepts at most one repo path", name)}
		default:
			repo = arg
			repoSet = true
		}
	}
	return repo, jsonOut, values, nil
}

func writeHookPauseStatus(stdout io.Writer, status agentsession.HookPauseStatus, jsonOut bool) error {
	if jsonOut {
		encoder := json.NewEncoder(stdout)
		encoder.SetIndent("", "  ")
		return encoder.Encode(status)
	}
	fmt.Fprintln(stdout, hookPauseLine(status))
	return nil
}

func hookPauseLine(status agentsession.HookPauseStatus) string {
	switch {
	case status.Active:
		return fmt.Sprintf("hook pause: active until %s (%s)", status.ExpiresAt, status.Reason)
	case status.PausedAt == "":
		return "hook pause: none"
	case status.ResumedAt != "":
		return fmt.Sprintf("hook pause: resumed at %s (paused %s: %s)", status.ResumedAt, status.PausedAt, status.Reason)
	default:
		return fmt.Sprintf("hook pause: expired at %s (%s)", status.ExpiresAt, status.Reason)
	}
}
