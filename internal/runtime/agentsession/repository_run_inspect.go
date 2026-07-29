package agentsession

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"reconc.dev/reconc/internal/filelock"
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
// error (returns nil). The JSONL writer lock is held while taking the snapshot,
// and malformed or truncated records fail closed rather than disappearing.
func ReadRunDecisions(repoRoot string, limit int) ([]RunDecision, error) {
	path, err := runDecisionLogPath(repoRoot)
	if err != nil {
		return nil, err
	}
	if _, err := os.Stat(filepath.Dir(path)); errors.Is(err, os.ErrNotExist) {
		return nil, nil
	} else if err != nil {
		return nil, err
	}
	lock, err := os.OpenFile(path+".lock", os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, err
	}
	defer lock.Close()
	unlock, err := filelock.Lock(lock)
	if err != nil {
		return nil, err
	}
	var out []RunDecision
	sources, err := jsonl.PathsOldestFirst(path, runDecisionMaxArchives)
	if err == nil {
		for _, source := range sources {
			if err = readRunDecisionFile(source, &out); err != nil {
				break
			}
		}
	}
	unlockErr := unlock()
	if err != nil {
		return out, err
	}
	if unlockErr != nil {
		return out, fmt.Errorf("unlock run decision log: %w", unlockErr)
	}
	if limit > 0 && len(out) > limit {
		out = out[len(out)-limit:]
	}
	return out, nil
}

func readRunDecisionFile(path string, out *[]RunDecision) error {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	if len(data) > 0 && data[len(data)-1] != '\n' {
		return fmt.Errorf("%s: truncated JSONL record: missing final newline", path)
	}
	for index, line := range bytes.Split(data, []byte{'\n'}) {
		if len(line) == 0 {
			continue
		}
		var d RunDecision
		decoder := json.NewDecoder(bytes.NewReader(line))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&d); err != nil {
			return fmt.Errorf("%s:%d: malformed run decision: %w", path, index+1, err)
		}
		var trailing interface{}
		if err := decoder.Decode(&trailing); err != io.EOF {
			if err == nil {
				err = errors.New("multiple JSON values are not allowed")
			}
			return fmt.Errorf("%s:%d: malformed run decision: %w", path, index+1, err)
		}
		*out = append(*out, d)
	}
	return nil
}
