package actionledger

import (
	"context"
	"fmt"

	"reconc.dev/reconc/internal/action"
)

type RecordingStatus string

const (
	RecordingRecorded RecordingStatus = "recorded"
	RecordingSkipped  RecordingStatus = "skipped"
	RecordingFailed   RecordingStatus = "failed"
)

type RecordingResult struct {
	Status           RecordingStatus   `json:"status"`
	Proceed          bool              `json:"proceed"`
	EvidenceComplete bool              `json:"evidence_complete"`
	Reason           action.ReasonCode `json:"reason_code,omitempty"`
	Record           *Record           `json:"record,omitempty"`
}

// Record applies the compiled recording mode. Required mode returns an error
// and Proceed=false on any append failure; best-effort mode exposes the exact
// failure reason without turning observation loss into dispatch authority.
func (s *Store) Record(ctx context.Context, mode action.LedgerMode, record Record) (RecordingResult, error) {
	if !mode.Valid() {
		return RecordingResult{Status: RecordingFailed, Proceed: false}, fmt.Errorf("action ledger mode is invalid")
	}
	if mode == action.LedgerOff {
		return RecordingResult{
			Status: RecordingSkipped, Proceed: true, EvidenceComplete: false,
		}, nil
	}
	sealed, err := s.Append(ctx, record)
	if err == nil {
		return RecordingResult{
			Status: RecordingRecorded, Proceed: true, EvidenceComplete: true, Record: &sealed,
		}, nil
	}
	result := RecordingResult{
		Status: RecordingFailed, Proceed: mode == action.LedgerBestEffort,
		EvidenceComplete: false, Reason: ErrorCode(err),
	}
	if mode == action.LedgerRequired {
		return result, err
	}
	return result, nil
}
