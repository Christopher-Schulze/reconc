package agentsession

import (
	"bufio"
	"encoding/json"
	"errors"
	"os"

	"reconc.dev/reconc/internal/jsonl"
	"reconc.dev/reconc/internal/tasklifecycle"
)

// RunLoopStatusInfo is the read-only snapshot rendered by `reconc run status`.
type RunLoopStatusInfo struct {
	Enabled              bool   `json:"enabled"`
	DisabledReason       string `json:"disabled_reason,omitempty"`
	AwaitingContinuation bool   `json:"awaiting_continuation"`
	NoProgressNudges     int    `json:"no_progress_nudges"`
	TaskDisposition      string `json:"task_disposition"`
	TaskID               string `json:"task_id,omitempty"`
	CurrentSubTask       string `json:"current_sub_task,omitempty"`
	Blocker              string `json:"blocker,omitempty"`
	OpenTasks            int    `json:"open_tasks"`
}

// RunLoopDecisionLogPath returns the repo-local decisions.jsonl path used by
// the repository run observability commands.
func RunLoopDecisionLogPath(repoRoot string) (string, error) {
	return runLoopDecisionLogPath(repoRoot)
}

// ReadRunLoopStatus loads the current repository run state for display. A missing
// state file is not an error: it returns a zero (disabled) snapshot.
func ReadRunLoopStatus(repoRoot string) (RunLoopStatusInfo, error) {
	state, err := loadRunLoopState(repoRoot)
	if err != nil {
		return RunLoopStatusInfo{}, err
	}
	taskState, err := tasklifecycle.InspectRunState(repoRoot)
	if err != nil {
		taskState = tasklifecycle.RunState{
			Disposition: tasklifecycle.RunInvalid,
			Blocker:     truncateBytes(err.Error(), 512),
		}
	}
	return RunLoopStatusInfo{
		Enabled:              state.Enabled,
		DisabledReason:       state.DisabledReason,
		AwaitingContinuation: state.AwaitingContinuation,
		NoProgressNudges:     state.NoProgressNudges,
		TaskDisposition:      string(taskState.Disposition),
		TaskID:               taskState.TaskID,
		CurrentSubTask:       taskState.SubTask,
		Blocker:              taskState.Blocker,
		OpenTasks:            taskState.OpenTasks,
	}, nil
}

// ReadRunLoopDecisions returns repository run decision records from
// .reconc/runloop/decisions.jsonl in chronological (append) order. When
// limit > 0 only the last limit records are returned. A missing log is not an
// error (returns nil). Malformed lines are skipped rather than failing the
// whole read, so a single bad append never blinds the observability surface.
func ReadRunLoopDecisions(repoRoot string, limit int) ([]RunLoopDecision, error) {
	path, err := runLoopDecisionLogPath(repoRoot)
	if err != nil {
		return nil, err
	}
	var out []RunLoopDecision
	for _, source := range jsonl.PathsOldestFirst(path, runLoopDecisionMaxArchives) {
		if err := readRunLoopDecisionFile(source, &out); err != nil {
			return out, err
		}
	}
	if limit > 0 && len(out) > limit {
		out = out[len(out)-limit:]
	}
	return out, nil
}

func readRunLoopDecisionFile(path string, out *[]RunLoopDecision) error {
	file, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 32*1024), 32*1024)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var d RunLoopDecision
		if err := json.Unmarshal(line, &d); err != nil {
			continue
		}
		*out = append(*out, d)
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	return nil
}
