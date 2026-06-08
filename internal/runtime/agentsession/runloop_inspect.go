package agentsession

import (
	"bufio"
	"encoding/json"
	"os"
)

// RunLoopStatusInfo is the read-only snapshot of the current runloop state
// rendered by `reconc runloop status`. It is derived from the same state
// file the runtime consumes, with the active stop-file already applied, so it
// reflects exactly what the next stop decision will see.
type RunLoopStatusInfo struct {
	Enabled              bool   `json:"enabled"`
	Runtime              string `json:"runtime,omitempty"`
	SessionID            string `json:"session_id,omitempty"`
	ActiveRunID          string `json:"active_run_id,omitempty"`
	DisabledReason       string `json:"disabled_reason,omitempty"`
	AwaitingContinuation bool   `json:"awaiting_continuation"`
	NoProgressNudges     int    `json:"no_progress_nudges"`
	StopFilePresent      bool   `json:"stop_file_present"`
}

// RunLoopDecisionLogPath returns the repo-local decisions.jsonl path used by
// the runloop observability commands.
func RunLoopDecisionLogPath(repoRoot string) (string, error) {
	return runLoopDecisionLogPath(repoRoot)
}

// ReadRunLoopStatus loads the current runloop state for display. A missing
// state file is not an error: it returns a zero (disabled) snapshot.
func ReadRunLoopStatus(repoRoot string) (RunLoopStatusInfo, error) {
	state, err := loadRunLoopState(repoRoot)
	if err != nil {
		return RunLoopStatusInfo{}, err
	}
	return RunLoopStatusInfo{
		Enabled:              state.Enabled,
		Runtime:              state.Runtime,
		SessionID:            state.SessionID,
		ActiveRunID:          state.ActiveRunID,
		DisabledReason:       state.DisabledReason,
		AwaitingContinuation: state.AwaitingContinuation,
		NoProgressNudges:     state.NoProgressNudges,
		StopFilePresent:      hasRunLoopStopFile(repoRoot),
	}, nil
}

// ReadRunLoopDecisions returns runloop decision records from
// .reconc/runloop/decisions.jsonl in chronological (append) order. When
// limit > 0 only the last limit records are returned. A missing log is not an
// error (returns nil). Malformed lines are skipped rather than failing the
// whole read, so a single bad append never blinds the observability surface.
func ReadRunLoopDecisions(repoRoot string, limit int) ([]RunLoopDecision, error) {
	path, err := runLoopDecisionLogPath(repoRoot)
	if err != nil {
		return nil, err
	}
	file, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer file.Close()

	var out []RunLoopDecision
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var d RunLoopDecision
		if err := json.Unmarshal(line, &d); err != nil {
			continue
		}
		out = append(out, d)
	}
	if err := scanner.Err(); err != nil {
		return out, err
	}
	if limit > 0 && len(out) > limit {
		out = out[len(out)-limit:]
	}
	return out, nil
}
