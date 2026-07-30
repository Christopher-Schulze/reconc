package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"reconc.dev/reconc/internal/tasklifecycle"
)

func runTask(args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return &CLIError{ExitCode: 1, Message: "reconc task: missing subcommand (status | validate | check-done | new | claim | block | resume | split | promote | archive | recover)"}
	}
	for _, arg := range args {
		if arg == "-h" || arg == "--help" {
			printTaskUsage(stdout)
			return nil
		}
	}
	subcommand := args[0]
	rest := args[1:]
	switch subcommand {
	case "status", "validate":
		return runTaskRead(subcommand, rest, stdout)
	case "check-done":
		return runTaskCheckDone(rest, stdout)
	case "new":
		return runTaskNew(rest, stdout)
	case "claim", "resume":
		return runTaskByID(subcommand, rest, stdout)
	case "block":
		return runTaskBlock(rest, stdout)
	case "split":
		return runTaskSplit(rest, stdout)
	case "promote":
		return runTaskPromote(rest, stdout)
	case "archive":
		return runTaskArchive(rest, stdout)
	case "recover":
		return runTaskRecover(rest, stdout)
	default:
		return &CLIError{ExitCode: 1, Message: fmt.Sprintf("reconc task: unknown subcommand %q", subcommand)}
	}
}

func printTaskUsage(stdout io.Writer) {
	fmt.Fprintln(stdout, "Usage:")
	fmt.Fprintln(stdout, "  reconc task status [repo] [--json]")
	fmt.Fprintln(stdout, "  reconc task validate [repo] [--json]")
	fmt.Fprintln(stdout, "  reconc task check-done [repo] [--task ID] [--json]")
	fmt.Fprintln(stdout, "  reconc task new [repo] --title TEXT [--id ID] [--json]")
	fmt.Fprintln(stdout, "  reconc task claim <ID> [repo] [--json]")
	fmt.Fprintln(stdout, "  reconc task block [repo] --reason TEXT [--next ID | --no-next] [--json]")
	fmt.Fprintln(stdout, "  reconc task resume <ID> [repo] [--json]")
	fmt.Fprintln(stdout, "  reconc task split [repo] --children ID,ID [--json]")
	fmt.Fprintln(stdout, "  reconc task promote [repo] [--next ID] [--json]")
	fmt.Fprintln(stdout, "  reconc task archive [repo] [--json]")
	fmt.Fprintln(stdout, "  reconc task recover [repo] [--json]")
	fmt.Fprintln(stdout, "")
	fmt.Fprintln(stdout, "Profiles: sections-v1 and logbook-v1, selected by task_lifecycle.profile or exact auto-detection.")
}

func runTaskNew(args []string, stdout io.Writer) error {
	repo := "."
	title := ""
	id := ""
	jsonOut := false
	seenRepo := false
	for index := 0; index < len(args); index++ {
		switch args[index] {
		case "--json":
			jsonOut = true
		case "--title", "--id":
			if index+1 >= len(args) {
				return taskCLIError("new", fmt.Errorf("%s requires a value", args[index]))
			}
			if args[index] == "--title" {
				title = args[index+1]
			} else {
				id = args[index+1]
			}
			index++
		default:
			if strings.HasPrefix(args[index], "-") {
				return taskCLIError("new", fmt.Errorf("unknown flag %q", args[index]))
			}
			if seenRepo {
				return taskCLIError("new", fmt.Errorf("unexpected argument %q", args[index]))
			}
			repo = args[index]
			seenRepo = true
		}
	}
	result, err := tasklifecycle.Create(repo, title, id)
	return writeTaskMutation("new", result, err, jsonOut, stdout)
}

func runTaskRead(subcommand string, args []string, stdout io.Writer) error {
	repo, jsonOut, err := parseTaskRepoJSON(args)
	if err != nil {
		return taskCLIError(subcommand, err)
	}
	board, err := tasklifecycle.Load(repo)
	if err != nil {
		return writeTaskFailure(subcommand, err, jsonOut, stdout)
	}
	if subcommand == "validate" {
		payload := map[string]any{"valid": true, "profile": board.Profile, "repo_root": board.RepoRoot}
		if jsonOut {
			return writeTaskJSON(stdout, payload)
		}
		fmt.Fprintf(stdout, "TASK lifecycle valid: %s (%s)\n", board.RepoRoot, board.Profile)
		return nil
	}
	briefing := tasklifecycle.BuildBriefing(board)
	if jsonOut {
		return writeTaskJSON(stdout, briefing)
	}
	writeTaskBriefing(stdout, briefing)
	return nil
}

func runTaskCheckDone(args []string, stdout io.Writer) error {
	repo := "."
	seenRepo := false
	id := ""
	jsonOut := false
	for index := 0; index < len(args); index++ {
		switch args[index] {
		case "--json":
			jsonOut = true
		case "--task":
			if index+1 >= len(args) {
				return taskCLIError("check-done", fmt.Errorf("--task requires an ID"))
			}
			id = args[index+1]
			index++
		default:
			if strings.HasPrefix(args[index], "-") {
				return taskCLIError("check-done", fmt.Errorf("unknown flag %q", args[index]))
			}
			if seenRepo {
				return taskCLIError("check-done", fmt.Errorf("unexpected argument %q", args[index]))
			}
			repo = args[index]
			seenRepo = true
		}
	}
	taskID, issues, err := tasklifecycle.CheckCompletion(repo, id)
	if err != nil {
		return writeTaskFailure("check-done", err, jsonOut, stdout)
	}
	if len(issues) > 0 {
		validationErr := &tasklifecycle.ValidationError{Issues: issues}
		return writeTaskFailure("check-done", validationErr, jsonOut, stdout)
	}
	if jsonOut {
		return writeTaskJSON(stdout, map[string]any{"ready": true, "task_id": taskID})
	}
	fmt.Fprintln(stdout, "TASK completion check: ready")
	return nil
}

func runTaskByID(subcommand string, args []string, stdout io.Writer) error {
	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		return taskCLIError(subcommand, fmt.Errorf("missing TASK ID"))
	}
	id := args[0]
	repo, jsonOut, err := parseTaskRepoJSON(args[1:])
	if err != nil {
		return taskCLIError(subcommand, err)
	}
	var result tasklifecycle.MutationResult
	if subcommand == "claim" {
		result, err = tasklifecycle.Claim(repo, id)
	} else {
		result, err = tasklifecycle.Resume(repo, id)
	}
	return writeTaskMutation(subcommand, result, err, jsonOut, stdout)
}

func runTaskBlock(args []string, stdout io.Writer) error {
	repo := "."
	seenRepo := false
	reason := ""
	next := ""
	noNext := false
	jsonOut := false
	for index := 0; index < len(args); index++ {
		switch args[index] {
		case "--json":
			jsonOut = true
		case "--no-next":
			noNext = true
		case "--reason", "--next":
			if index+1 >= len(args) {
				return taskCLIError("block", fmt.Errorf("%s requires a value", args[index]))
			}
			if args[index] == "--reason" {
				reason = args[index+1]
			} else {
				next = args[index+1]
			}
			index++
		default:
			if strings.HasPrefix(args[index], "-") {
				return taskCLIError("block", fmt.Errorf("unknown flag %q", args[index]))
			}
			if seenRepo {
				return taskCLIError("block", fmt.Errorf("unexpected argument %q", args[index]))
			}
			repo = args[index]
			seenRepo = true
		}
	}
	if noNext && next != "" {
		return taskCLIError("block", fmt.Errorf("--next and --no-next are mutually exclusive"))
	}
	var result tasklifecycle.MutationResult
	var err error
	if noNext {
		result, err = tasklifecycle.BlockWithoutNext(repo, reason)
	} else {
		result, err = tasklifecycle.Block(repo, reason, next)
	}
	return writeTaskMutation("block", result, err, jsonOut, stdout)
}

func runTaskSplit(args []string, stdout io.Writer) error {
	repo := "."
	seenRepo := false
	children := []string{}
	jsonOut := false
	for index := 0; index < len(args); index++ {
		switch args[index] {
		case "--json":
			jsonOut = true
		case "--children":
			if index+1 >= len(args) {
				return taskCLIError("split", fmt.Errorf("--children requires a comma-separated value"))
			}
			children = splitCommaList(args[index+1])
			index++
		default:
			if strings.HasPrefix(args[index], "-") {
				return taskCLIError("split", fmt.Errorf("unknown flag %q", args[index]))
			}
			if seenRepo {
				return taskCLIError("split", fmt.Errorf("unexpected argument %q", args[index]))
			}
			repo = args[index]
			seenRepo = true
		}
	}
	result, err := tasklifecycle.Split(repo, children)
	return writeTaskMutation("split", result, err, jsonOut, stdout)
}

func runTaskPromote(args []string, stdout io.Writer) error {
	repo := "."
	seenRepo := false
	next := ""
	jsonOut := false
	for index := 0; index < len(args); index++ {
		switch args[index] {
		case "--json":
			jsonOut = true
		case "--next":
			if index+1 >= len(args) {
				return taskCLIError("promote", fmt.Errorf("--next requires an ID"))
			}
			next = args[index+1]
			index++
		default:
			if strings.HasPrefix(args[index], "-") {
				return taskCLIError("promote", fmt.Errorf("unknown flag %q", args[index]))
			}
			if seenRepo {
				return taskCLIError("promote", fmt.Errorf("unexpected argument %q", args[index]))
			}
			repo = args[index]
			seenRepo = true
		}
	}
	result, err := tasklifecycle.Promote(repo, next)
	return writeTaskMutation("promote", result, err, jsonOut, stdout)
}

func runTaskArchive(args []string, stdout io.Writer) error {
	repo, jsonOut, err := parseTaskRepoJSON(args)
	if err != nil {
		return taskCLIError("archive", err)
	}
	result, err := tasklifecycle.Archive(repo)
	return writeTaskMutation("archive", result, err, jsonOut, stdout)
}

func runTaskRecover(args []string, stdout io.Writer) error {
	repo, jsonOut, err := parseTaskRepoJSON(args)
	if err != nil {
		return taskCLIError("recover", err)
	}
	abs, err := filepath.Abs(repo)
	recovered := false
	if err == nil {
		recovered, err = tasklifecycle.RecoverIfNeeded(abs)
	}
	if err != nil {
		return writeTaskFailure("recover", err, jsonOut, stdout)
	}
	if jsonOut {
		return writeTaskJSON(stdout, map[string]any{"action": "recover", "recovered": recovered})
	}
	if !recovered {
		fmt.Fprintln(stdout, "No interrupted TASK transaction found")
		return nil
	}
	fmt.Fprintln(stdout, "TASK transaction recovered to its pre-mutation state")
	return nil
}

func parseTaskRepoJSON(args []string) (string, bool, error) {
	repo := "."
	jsonOut := false
	seenRepo := false
	for _, arg := range args {
		switch {
		case arg == "--json":
			jsonOut = true
		case strings.HasPrefix(arg, "-"):
			return "", false, fmt.Errorf("unknown flag %q", arg)
		case seenRepo:
			return "", false, fmt.Errorf("unexpected argument %q", arg)
		default:
			repo = arg
			seenRepo = true
		}
	}
	return repo, jsonOut, nil
}

func writeTaskMutation(subcommand string, result tasklifecycle.MutationResult, err error, jsonOut bool, stdout io.Writer) error {
	if err != nil {
		return writeTaskFailure(subcommand, err, jsonOut, stdout)
	}
	if jsonOut {
		return writeTaskJSON(stdout, result)
	}
	if result.PreviousState == "" {
		fmt.Fprintf(stdout, "TASK %s: %s -> %s", result.Action, result.TaskID, result.State)
	} else {
		fmt.Fprintf(stdout, "TASK %s: %s %s -> %s", result.Action, result.TaskID, result.PreviousState, result.State)
	}
	if result.NextTaskID != "" {
		fmt.Fprintf(stdout, "; active=%s", result.NextTaskID)
	}
	fmt.Fprintln(stdout)
	return nil
}

func writeTaskFailure(subcommand string, err error, jsonOut bool, stdout io.Writer) error {
	var validationErr *tasklifecycle.ValidationError
	if jsonOut && errors.As(err, &validationErr) {
		_ = writeTaskJSON(stdout, map[string]any{"valid": false, "issues": validationErr.Issues})
	}
	return &CLIError{ExitCode: 2, Message: "reconc task " + subcommand + ": " + err.Error()}
}

func taskCLIError(subcommand string, err error) error {
	return &CLIError{ExitCode: 1, Message: "reconc task " + subcommand + ": " + err.Error()}
}

func writeTaskJSON(stdout io.Writer, value any) error {
	encoder := json.NewEncoder(stdout)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(value); err != nil {
		return &CLIError{ExitCode: 1, Message: "reconc task: encode JSON: " + err.Error()}
	}
	return nil
}

func writeTaskBriefing(stdout io.Writer, briefing tasklifecycle.Briefing) {
	fmt.Fprintf(stdout, "TASK lifecycle: %s\n", briefing.Profile)
	if briefing.Current == nil {
		fmt.Fprintln(stdout, "  Current: none")
	} else {
		fmt.Fprintf(stdout, "  Current: %s %s -> %s\n", briefing.Current.ID, briefing.Current.Title, briefing.Current.Path)
		if briefing.Current.CurrentSubTask != "" {
			fmt.Fprintf(stdout, "  Sub-Task: %s\n", briefing.Current.CurrentSubTask)
		}
	}
	for _, blocker := range briefing.Blockers {
		fmt.Fprintf(stdout, "  Blocked: %s %s\n", blocker.ID, blocker.Reason)
	}
	if briefing.OmittedBlockers > 0 {
		fmt.Fprintf(stdout, "  Blocked: +%d more\n", briefing.OmittedBlockers)
	}
	if len(briefing.RequiredEvidence) > 0 {
		fmt.Fprintf(stdout, "  Evidence: %s\n", strings.Join(briefing.RequiredEvidence, ", "))
	}
	if briefing.OmittedEvidence > 0 {
		fmt.Fprintf(stdout, "  Evidence: +%d more\n", briefing.OmittedEvidence)
	}
	fmt.Fprintf(stdout, "  Next: %s\n", briefing.Remediation)
}
