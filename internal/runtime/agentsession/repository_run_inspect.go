package agentsession

import (
	"bufio"
	"encoding/json"
	"errors"
	"os"

	"reconc.dev/reconc/internal/jsonl"
	"reconc.dev/reconc/internal/tasklifecycle"
)

// RepositoryRunStatus is the read-only snapshot rendered by `reconc run status`.
type RepositoryRunStatus struct {
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

// RunDecisionLogPath returns the repo-local decisions.jsonl path used by
// the repository run observability commands.
func RunDecisionLogPath(repoRoot string) (string, error) {
	return runDecisionLogPath(repoRoot)
}

// ReadRepositoryRunStatus loads repository run state for display. A missing
// state file is not an error: it returns a zero (disabled) snapshot.
func ReadRepositoryRunStatus(repoRoot string) (RepositoryRunStatus, error) {
	root, err := ResolveRepoRoot(repoRoot)
	if err != nil {
		return RepositoryRunStatus{}, err
	}
	return readRepositoryRunStatusResolved(root)
}

func readRepositoryRunStatusResolved(root string) (RepositoryRunStatus, error) {
	state, err := loadRepositoryRunStateResolved(root)
	if err != nil {
		return RepositoryRunStatus{}, err
	}
	taskState, err := tasklifecycle.InspectRunStateResolved(root)
	if err != nil {
		taskState = tasklifecycle.RunState{
			Disposition: tasklifecycle.RunInvalid,
			Blocker:     truncateBytes(err.Error(), 512),
		}
	}
	return RepositoryRunStatus{
		Enabled:              state.Enabled,
		DisabledReason:       state.DisabledReason.String(),
		AwaitingContinuation: state.AwaitingContinuation,
		NoProgressNudges:     state.NoProgressNudges,
		TaskDisposition:      string(taskState.Disposition),
		TaskID:               taskState.TaskID,
		CurrentSubTask:       taskState.SubTask,
		Blocker:              taskState.Blocker,
		OpenTasks:            taskState.OpenTasks,
	}, nil
}

// ReadRunDecisions returns repository run decision records from
// .reconc/run/decisions.jsonl in chronological (append) order. When
// limit > 0 only the last limit records are returned. A missing log is not an
// error (returns nil). Malformed lines are skipped rather than failing the
// whole read, so a single bad append never blinds the observability surface.
func ReadRunDecisions(repoRoot string, limit int) ([]RunDecision, error) {
	path, err := runDecisionLogPath(repoRoot)
	if err != nil {
		return nil, err
	}
	var out []RunDecision
	for _, source := range jsonl.PathsOldestFirst(path, runDecisionMaxArchives) {
		if err := readRunDecisionFile(source, &out); err != nil {
			return out, err
		}
	}
	if limit > 0 && len(out) > limit {
		out = out[len(out)-limit:]
	}
	return out, nil
}

func readRunDecisionFile(path string, out *[]RunDecision) error {
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
		var d RunDecision
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
