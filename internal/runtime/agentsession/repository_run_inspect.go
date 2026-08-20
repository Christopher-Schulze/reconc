package agentsession

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"reconc.dev/reconc/internal/boundedio"
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
	if limit < 0 {
		return nil, fmt.Errorf("run decision limit must be non-negative")
	}
	root, err := ResolveRepoRoot(repoRoot)
	if err != nil {
		return nil, err
	}
	path := runDecisionLogPathResolved(root)
	if err := validateRepositoryRunStatePath(root, path); err != nil {
		return nil, err
	}
	directoryInfo, err := os.Lstat(filepath.Dir(path))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	} else if err != nil {
		return nil, err
	}
	if directoryInfo.Mode()&os.ModeSymlink != 0 || !directoryInfo.IsDir() {
		return nil, fmt.Errorf("repository run log parent must be a non-symlink directory")
	}
	lockPath := path + ".lock"
	if info, lstatErr := os.Lstat(lockPath); lstatErr == nil && (info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular()) {
		return nil, fmt.Errorf("repository run log lock must be a non-symlink regular file")
	} else if lstatErr != nil && !errors.Is(lstatErr, os.ErrNotExist) {
		return nil, lstatErr
	}
	lock, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, err
	}
	defer lock.Close()
	unlock, err := filelock.LockContext(context.Background(), lock, agentSessionLockTimeout)
	if err != nil {
		return nil, err
	}
	collector := runDecisionCollector{limit: limit}
	sources, err := jsonl.PathsOldestFirst(path, runDecisionMaxArchives)
	if err == nil {
		for _, source := range sources {
			if err = readRunDecisionFile(source, collector.add); err != nil {
				break
			}
		}
	}
	unlockErr := unlock()
	if err != nil {
		return nil, err
	}
	if unlockErr != nil {
		return collector.values(), fmt.Errorf("unlock run decision log: %w", unlockErr)
	}
	return collector.values(), nil
}

const runDecisionMaxRecordBytes = 32 * 1024

type runDecisionCollector struct {
	limit   int
	items   []RunDecision
	next    int
	wrapped bool
}

func (collector *runDecisionCollector) add(decision RunDecision) {
	if collector.limit == 0 || len(collector.items) < collector.limit {
		collector.items = append(collector.items, decision)
		return
	}
	collector.items[collector.next] = decision
	collector.next = (collector.next + 1) % collector.limit
	collector.wrapped = true
}

func (collector *runDecisionCollector) values() []RunDecision {
	if !collector.wrapped || collector.next == 0 {
		return collector.items
	}
	out := make([]RunDecision, 0, len(collector.items))
	out = append(out, collector.items[collector.next:]...)
	out = append(out, collector.items[:collector.next]...)
	return out
}

func readRunDecisionFile(path string, emit func(RunDecision)) error {
	return boundedio.WithRegularFileSnapshot(path, runDecisionMaxBytes, func(file *os.File, info os.FileInfo) error {
		if info.Size() == 0 {
			return nil
		}
		if _, err := file.Seek(-1, io.SeekEnd); err != nil {
			return err
		}
		last := []byte{0}
		if _, err := io.ReadFull(file, last); err != nil {
			return err
		}
		if last[0] != '\n' {
			return fmt.Errorf("%s: truncated JSONL record: missing final newline", path)
		}
		if _, err := file.Seek(0, io.SeekStart); err != nil {
			return err
		}
		scanner := bufio.NewScanner(file)
		scanner.Buffer(make([]byte, 4096), runDecisionMaxRecordBytes)
		lineNumber := 0
		for scanner.Scan() {
			lineNumber++
			line := scanner.Bytes()
			if len(bytes.TrimSpace(line)) == 0 {
				continue
			}
			d, err := decodeRunDecisionLine(line)
			if err != nil {
				return fmt.Errorf("%s:%d: malformed run decision: %w", path, lineNumber, err)
			}
			emit(d)
		}
		if err := scanner.Err(); err != nil {
			return fmt.Errorf("%s: read run decisions: %w", path, err)
		}
		return nil
	})
}

func decodeRunDecisionLine(line []byte) (RunDecision, error) {
	var decision RunDecision
	decoder := json.NewDecoder(bytes.NewReader(line))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&decision); err != nil {
		return RunDecision{}, err
	}
	var trailing interface{}
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			err = errors.New("multiple JSON values are not allowed")
		}
		return RunDecision{}, err
	}
	return decision, nil
}
