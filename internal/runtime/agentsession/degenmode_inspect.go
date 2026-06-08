package agentsession

import (
	"bufio"
	"encoding/json"
	"os"
)

// DegenModeStatusInfo is the read-only snapshot of the current degenmode state
// rendered by `reconc degenmode status`. It is derived from the same state
// file the runtime consumes, with the active stop-file already applied, so it
// reflects exactly what the next stop decision will see.
type DegenModeStatusInfo struct {
	Enabled              bool   `json:"enabled"`
	Runtime              string `json:"runtime,omitempty"`
	SessionID            string `json:"session_id,omitempty"`
	ActiveRunID          string `json:"active_run_id,omitempty"`
	DisabledReason       string `json:"disabled_reason,omitempty"`
	AwaitingContinuation bool   `json:"awaiting_continuation"`
	NoProgressNudges     int    `json:"no_progress_nudges"`
	StopFilePresent      bool   `json:"stop_file_present"`
}

// DegenModeDecisionLogPath returns the repo-local decisions.jsonl path used by
// the degenmode observability commands.
func DegenModeDecisionLogPath(repoRoot string) (string, error) {
	return degenModeDecisionLogPath(repoRoot)
}

// ReadDegenModeStatus loads the current degenmode state for display. A missing
// state file is not an error: it returns a zero (disabled) snapshot.
func ReadDegenModeStatus(repoRoot string) (DegenModeStatusInfo, error) {
	state, err := loadDegenModeState(repoRoot)
	if err != nil {
		return DegenModeStatusInfo{}, err
	}
	return DegenModeStatusInfo{
		Enabled:              state.Enabled,
		Runtime:              state.Runtime,
		SessionID:            state.SessionID,
		ActiveRunID:          state.ActiveRunID,
		DisabledReason:       state.DisabledReason,
		AwaitingContinuation: state.AwaitingContinuation,
		NoProgressNudges:     state.NoProgressNudges,
		StopFilePresent:      hasDegenModeStopFile(repoRoot),
	}, nil
}

// ReadDegenModeDecisions returns degenmode decision records from
// .reconc/degenmode/decisions.jsonl in chronological (append) order. When
// limit > 0 only the last limit records are returned. A missing log is not an
// error (returns nil). Malformed lines are skipped rather than failing the
// whole read, so a single bad append never blinds the observability surface.
func ReadDegenModeDecisions(repoRoot string, limit int) ([]DegenModeDecision, error) {
	path, err := degenModeDecisionLogPath(repoRoot)
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

	var out []DegenModeDecision
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var d DegenModeDecision
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
