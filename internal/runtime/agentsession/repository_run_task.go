package agentsession

import (
	"crypto/sha256"
	"fmt"
	"strconv"
	"strings"

	"reconc.dev/reconc/internal/tasklifecycle"
)

const maxRepositoryRunPromptBytes = 1600

func inspectRepositoryRunTask(repoRoot string) (tasklifecycle.RunState, error) {
	return tasklifecycle.InspectRunStateResolved(repoRoot)
}

func buildRepositoryRunPrompt(state tasklifecycle.RunState) string {
	var prompt string
	switch state.Disposition {
	case tasklifecycle.RunContinue:
		prompt = fmt.Sprintf("Reconc run is ON. Continue TASK %s: %s.", state.TaskID, state.TaskTitle)
		if state.SubTask != "" {
			prompt += " Current Sub-Task: " + state.SubTask + "."
		}
		prompt += " Execute Reconc commands yourself. Run `reconc task check-done .` before `reconc task promote .`. Use `reconc run off .` only for an explicit user stop or a real blocker."
	case tasklifecycle.RunClaim:
		prompt = fmt.Sprintf("Reconc run is ON and no TASK is active. Execute `reconc task claim %s .`, then continue %s. Do not ask the user to operate Reconc.", state.TaskID, state.TaskTitle)
	default:
		return ""
	}
	return truncateBytes(strings.TrimSpace(prompt), maxRepositoryRunPromptBytes)
}

func repositoryRunProgressFingerprint(state tasklifecycle.RunState) string {
	return strings.Join([]string{
		string(state.Disposition), state.TaskID, state.TaskPath,
		state.SubTask, fmt.Sprintf("%d", state.OpenTasks),
	}, "\n")
}

func repositoryRunProgressHash(state tasklifecycle.RunState, materialEvents uint64) [sha256.Size]byte {
	fingerprint := repositoryRunProgressFingerprint(state)
	buffer := make([]byte, 0, len(fingerprint)+1+20)
	buffer = append(buffer, fingerprint...)
	buffer = append(buffer, '\n')
	buffer = strconv.AppendUint(buffer, materialEvents, 10)
	return sha256.Sum256(buffer)
}

type repositoryRunTaskState struct {
	tasklifecycle.RunState
}

func (state repositoryRunTaskState) executable() bool {
	return state.Disposition == tasklifecycle.RunContinue || state.Disposition == tasklifecycle.RunClaim
}

func repositoryRunTerminalReason(state tasklifecycle.RunState) repositoryRunDisabledReason {
	switch state.Disposition {
	case tasklifecycle.RunBlocked:
		return repositoryRunDisabledBlockedTask
	case tasklifecycle.RunComplete:
		return repositoryRunDisabledTaskComplete
	case tasklifecycle.RunAbsent:
		return repositoryRunDisabledTaskPlaneAbsent
	default:
		return repositoryRunDisabledNoExecutableTask
	}
}
