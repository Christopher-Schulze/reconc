package actionledger

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"reconc.dev/reconc/internal/action"
	"reconc.dev/reconc/internal/jsonl"
)

type VerificationStatus string

const (
	StatusEmpty    VerificationStatus = "empty"
	StatusVerified VerificationStatus = "verified"
	StatusInvalid  VerificationStatus = "invalid"
)

type HeadStatus string

const (
	HeadAbsent  HeadStatus = "absent"
	HeadMatched HeadStatus = "matched"
	HeadInvalid HeadStatus = "invalid"
)

type VerificationReport struct {
	FormatVersion         string             `json:"format_version"`
	Integrity             VerificationStatus `json:"integrity"`
	ArchiveContinuity     VerificationStatus `json:"archive_continuity"`
	DetachedHead          HeadStatus         `json:"detached_head"`
	RecordCount           uint64             `json:"record_count"`
	ArchiveCount          uint32             `json:"archive_count"`
	FirstRetainedSequence uint64             `json:"first_retained_sequence,omitempty"`
	LastRetainedSequence  uint64             `json:"last_retained_sequence,omitempty"`
	FirstRecordedSequence uint64             `json:"first_recorded_sequence,omitempty"`
	DroppedHistory        bool               `json:"dropped_history"`
	DroppedBeforeSequence uint64             `json:"dropped_before_sequence,omitempty"`
	EventsEvaluated       bool               `json:"events_evaluated"`
	EventsComplete        bool               `json:"events_complete"`
	IncompleteEvents      uint64             `json:"incomplete_events"`
	CallsEvaluated        bool               `json:"calls_evaluated"`
	CallsComplete         bool               `json:"calls_complete"`
	IncompleteCalls       uint64             `json:"incomplete_calls"`
}

func (s *Store) Verify(ctx context.Context) (VerificationReport, error) {
	_, report, err := s.Snapshot(ctx)
	return report, err
}

// Snapshot returns a fully verified, oldest-first retained record snapshot.
// Callers never receive records from an invalid chain or archive set.
func (s *Store) Snapshot(ctx context.Context) ([]Record, VerificationReport, error) {
	report := newVerificationReport()
	if s == nil || ctx == nil {
		report.Integrity = StatusInvalid
		return nil, report, fmt.Errorf("action ledger store and context are required")
	}
	if err := s.storage.Validate(); err != nil {
		report.Integrity = StatusInvalid
		return nil, report, err
	}
	lock, err := s.validateExistingLock(ctx)
	if err != nil {
		report.Integrity = StatusInvalid
		return nil, report, err
	}
	if !lock {
		exists, existingErr := s.ExistingState(ctx)
		if existingErr != nil {
			report.Integrity = StatusInvalid
			return nil, report, existingErr
		}
		if !exists {
			return []Record{}, EmptyVerificationReport(), nil
		}
	}
	var snapshot []Record
	var verificationErr error
	err = jsonl.ReadExistingSnapshotContextWithLayout(ctx, s.livePath, s.layout, s.commitHeadLocked, func() error {
		if err := s.validateStablePathsAfterRecovery(); err != nil {
			report.Integrity = StatusInvalid
			verificationErr = err
			return nil
		}
		records, head, verified, err := s.loadVerifiedLocked()
		report = verified
		if err != nil {
			verificationErr = err
			return nil
		}
		snapshot = records
		_ = head
		return nil
	})
	if err != nil {
		if report.Integrity != StatusInvalid {
			report.Integrity = StatusInvalid
		}
		return nil, report, err
	}
	if verificationErr != nil {
		return nil, report, verificationErr
	}
	return snapshot, report, nil
}

func newVerificationReport() VerificationReport {
	return VerificationReport{
		FormatVersion: FormatVersion, Integrity: StatusEmpty, ArchiveContinuity: StatusEmpty,
		DetachedHead: HeadAbsent,
	}
}

func EmptyVerificationReport() VerificationReport {
	report := newVerificationReport()
	report.EventsEvaluated = true
	report.EventsComplete = true
	report.CallsEvaluated = true
	report.CallsComplete = true
	return report
}

func (s *Store) loadVerifiedLocked() ([]Record, *chainHead, VerificationReport, error) {
	report := newVerificationReport()
	records, err := s.loadRecordsLocked()
	if err != nil {
		report.Integrity = StatusInvalid
		return nil, nil, report, err
	}
	head, err := s.loadHead()
	if err != nil {
		report.Integrity = StatusInvalid
		report.DetachedHead = HeadInvalid
		return nil, nil, report, err
	}
	if len(records) == 0 {
		if head != nil {
			report.Integrity = StatusInvalid
			report.DetachedHead = HeadInvalid
			return nil, head, report, fmt.Errorf("action ledger detached head exists without retained records")
		}
		if _, checkpointErr := os.Lstat(s.checkpointPath); checkpointErr == nil {
			return nil, nil, report, fmt.Errorf("action ledger checkpoint exists without retained records")
		} else if !errors.Is(checkpointErr, os.ErrNotExist) {
			return nil, nil, report, fmt.Errorf("inspect action ledger checkpoint: %w", checkpointErr)
		}
		return []Record{}, nil, EmptyVerificationReport(), nil
	}
	report.RecordCount = uint64(len(records))
	report.ArchiveCount = s.archiveCount()
	report.FirstRetainedSequence = records[0].Sequence
	report.LastRetainedSequence = records[len(records)-1].Sequence
	for index, record := range records {
		if record.Call.RepositoryIdentity != s.storage.RepositoryIdentity() {
			report.Integrity = StatusInvalid
			return nil, head, report, fmt.Errorf("action ledger record repository identity drifted at sequence %d", record.Sequence)
		}
		if err := s.validateRecordKeyGeneration(record); err != nil {
			report.Integrity = StatusInvalid
			return nil, head, report, fmt.Errorf("action ledger identity generation drifted at sequence %d: %w", record.Sequence, err)
		}
		if index > 0 {
			previous := records[index-1]
			if record.Sequence != previous.Sequence+1 || record.PreviousDigest != previous.Digest {
				report.Integrity = StatusInvalid
				report.ArchiveContinuity = StatusInvalid
				return nil, head, report, fmt.Errorf("action ledger chain is discontinuous at sequence %d", record.Sequence)
			}
		}
	}
	report.ArchiveContinuity = StatusVerified
	report.EventsEvaluated = true
	report.EventsComplete = true
	for _, record := range records {
		if !record.Decision.Completeness.Complete() || selectedEvidenceIncomplete(record.SelectedFields) {
			report.EventsComplete = false
			report.IncompleteEvents++
		}
	}
	statuses, err := BuildCallStatuses(records)
	if err != nil {
		report.Integrity = StatusInvalid
		return nil, head, report, err
	}
	report.CallsEvaluated = true
	report.CallsComplete = true
	for _, status := range statuses {
		if !status.TerminalComplete || !status.EvidenceComplete {
			report.CallsComplete = false
			report.IncompleteCalls++
		}
	}
	if head == nil {
		report.Integrity = StatusInvalid
		report.DetachedHead = HeadAbsent
		return nil, nil, report, fmt.Errorf("action ledger retained records have no detached head")
	}
	first := records[0]
	last := records[len(records)-1]
	if head.FirstSequence != 1 || head.EntryCount != head.LastSequence ||
		head.LastSequence != last.Sequence || head.LastDigest != last.Digest ||
		head.UpdatedAt != last.Timestamp || head.RepositoryIdentity != s.storage.RepositoryIdentity() {
		report.Integrity = StatusInvalid
		report.DetachedHead = HeadInvalid
		return nil, head, report, fmt.Errorf("action ledger detached head does not match the retained chain")
	}
	if first.Sequence == 1 {
		if first.PreviousDigest != "" || head.FirstDigest != first.Digest {
			report.Integrity = StatusInvalid
			report.DetachedHead = HeadInvalid
			return nil, head, report, fmt.Errorf("action ledger first record does not match the detached head")
		}
	} else {
		report.DroppedHistory = true
		report.DroppedBeforeSequence = first.Sequence
	}
	report.FirstRecordedSequence = head.FirstSequence
	report.DetachedHead = HeadMatched
	report.Integrity = StatusVerified
	return records, head, report, nil
}

func selectedEvidenceIncomplete(fields []SelectedFieldEvidence) bool {
	for _, field := range fields {
		if !field.Complete {
			return true
		}
	}
	return false
}

func (s *Store) loadRecordsLocked() ([]Record, error) {
	if err := s.validateArchiveSet(); err != nil {
		return nil, err
	}
	paths, err := jsonl.PathsOldestFirst(s.livePath, s.policy.MaxArchives)
	if err != nil {
		return nil, ledgerError(action.ReasonLedgerUnavailable, "inspect action ledger files", err)
	}
	records := make([]Record, 0)
	for _, path := range paths {
		body, err := s.storage.ReadPrivateFile(filepath.Base(path), MaxLiveBytes)
		if err != nil {
			return nil, ledgerError(action.ReasonLedgerUnavailable, "read action ledger file "+filepath.Base(path), err)
		}
		if len(body) == 0 || body[len(body)-1] != '\n' {
			return nil, ledgerError(action.ReasonLedgerCorrupt, "action ledger file "+filepath.Base(path)+" is empty or has a partial tail", nil)
		}
		lines := bytes.Split(body[:len(body)-1], []byte{'\n'})
		for index, line := range lines {
			if len(line) == 0 {
				return nil, ledgerError(action.ReasonLedgerCorrupt, fmt.Sprintf("action ledger file %s contains an empty record at line %d", filepath.Base(path), index+1), nil)
			}
			record, err := Decode(line)
			if err != nil {
				return nil, ledgerError(action.ReasonLedgerCorrupt, fmt.Sprintf("decode action ledger file %s line %d", filepath.Base(path), index+1), err)
			}
			records = append(records, record)
		}
	}
	return records, nil
}

func (s *Store) validateArchiveSet() error {
	exists := make([]bool, s.policy.MaxArchives+1)
	for index := 0; index <= s.policy.MaxArchives; index++ {
		path := s.livePath
		if index > 0 {
			path = s.livePath + fmt.Sprintf(".%d", index)
		}
		info, err := os.Lstat(path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return ledgerError(action.ReasonLedgerUnavailable, "inspect action ledger file", err)
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || info.Size() > MaxLiveBytes {
			return ledgerError(action.ReasonLedgerUnavailable, "action ledger file must be a bounded non-symlink regular file: "+path, nil)
		}
		if err := s.storage.ValidateJSONLFile(path, MaxLiveBytes); err != nil {
			return ledgerError(action.ReasonLedgerUnavailable, "validate private action ledger file "+filepath.Base(path), err)
		}
		exists[index] = true
	}
	if !archiveSetContiguous(exists) {
		return ledgerError(action.ReasonLedgerCorrupt, "action ledger archive set has a gap", nil)
	}
	return nil
}

func (s *Store) validateStablePathsAfterRecovery() error {
	entries, err := s.readActionDirectorySnapshot()
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if !bytes.HasPrefix([]byte(entry.Name()), []byte("ledger")) {
			continue
		}
		if !allowedLedgerEntry(entry.Name(), false, s.policy.MaxArchives) {
			return fmt.Errorf("unexpected action ledger entry %s after recovery", entry.Name())
		}
		if entry.Name() == checkpointFileName {
			if err := s.storage.ValidateJSONLFile(s.checkpointPath, maxCheckpointBytes); err != nil {
				return fmt.Errorf("validate private action ledger checkpoint: %w", err)
			}
		}
	}
	return s.validateArchiveSet()
}

func (s *Store) archiveCount() uint32 {
	count := uint32(0)
	for index := 1; index <= s.policy.MaxArchives; index++ {
		if _, err := os.Lstat(fmt.Sprintf("%s.%d", s.livePath, index)); err == nil {
			count++
		}
	}
	return count
}

func archiveSetContiguous(exists []bool) bool {
	for index := 1; index < len(exists); index++ {
		if exists[index] && !exists[index-1] {
			return false
		}
	}
	return true
}
