package agentsession

import (
	"fmt"
	"strings"

	"reconc.dev/reconc/internal/tasklifecycle"
)

const maxRunLoopPromptBytes = 1600

func inspectRunLoopTask(repoRoot string) (tasklifecycle.RunState, error) {
	return tasklifecycle.InspectRunState(repoRoot)
}

func buildRunLoopContinuationPrompt(state tasklifecycle.RunState) string {
	var prompt string
	switch state.Disposition {
	case tasklifecycle.RunContinue:
		prompt = fmt.Sprintf("Runloop autocontinue. LET ME COOK. Reconc run is ON. Continue TASK %s: %s.", state.TaskID, state.TaskTitle)
		if state.SubTask != "" {
			prompt += " Current Sub-Task: " + state.SubTask + "."
		}
		prompt += " Execute Reconc commands yourself. Run `reconc task check-done .` before `reconc task promote .`. Use `reconc run off .` only for an explicit user stop or a real blocker."
	case tasklifecycle.RunClaim:
		prompt = fmt.Sprintf("Runloop autocontinue. LET ME COOK. Reconc run is ON and no TASK is active. Execute `reconc task claim %s .`, then continue %s. Do not ask the user to operate Reconc.", state.TaskID, state.TaskTitle)
	default:
		return ""
	}
	return truncateBytes(strings.TrimSpace(prompt), maxRunLoopPromptBytes)
}

func runLoopTaskProgressFingerprint(state tasklifecycle.RunState) string {
	return strings.Join([]string{
		string(state.Disposition), state.TaskID, state.TaskPath,
		state.SubTask, fmt.Sprintf("%d", state.OpenTasks),
	}, "\n")
}

type runLoopTaskState struct {
	tasklifecycle.RunState
}

func (state runLoopTaskState) executable() bool {
	return state.Disposition == tasklifecycle.RunContinue || state.Disposition == tasklifecycle.RunClaim
}

func runLoopTerminalReason(state tasklifecycle.RunState) string {
	switch state.Disposition {
	case tasklifecycle.RunBlocked:
		return "blocked_task"
	case tasklifecycle.RunComplete:
		return "task_complete"
	case tasklifecycle.RunAbsent:
		return "task_plane_absent"
	default:
		return "no_executable_task"
	}
}
