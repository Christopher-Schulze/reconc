package actionledger

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"

	"reconc.dev/reconc/internal/actionstate"
	"reconc.dev/reconc/internal/jsonl"
)

const (
	checkpointFileName = "ledger.checkpoint.json"
	checkpointSchema   = "reconc.action-ledger-checkpoint/v1"
	maxCheckpointBytes = 16 << 20
)

type ledgerFileCheckpoint struct {
	Name       string `json:"name"`
	Generation string `json:"generation"`
	Size       int64  `json:"size"`
}

type ledgerCheckpointPayload struct {
	Schema             string                 `json:"schema"`
	RepositoryIdentity string                 `json:"repository_identity"`
	KeyID              string                 `json:"key_id"`
	Head               chainHead              `json:"head"`
	LastRecord         Record                 `json:"last_record"`
	Files              []ledgerFileCheckpoint `json:"files"`
	ActiveRecords      []Record               `json:"active_records"`
	TerminalCallCount  uint64                 `json:"terminal_call_count"`
	TerminalCallDigest string                 `json:"terminal_call_digest"`
}

type ledgerCheckpointEnvelope struct {
	Payload  ledgerCheckpointPayload `json:"payload"`
	Identity string                  `json:"identity"`
}

type ledgerCheckpointCache struct {
	payload              ledgerCheckpointPayload
	checkpointGeneration string
	terminalCallIDs      map[string]struct{}
}

func (s *Store) checkpointFromRecords(
	records []Record,
	head *chainHead,
) (ledgerCheckpointPayload, []CallStatus, map[string]struct{}, error) {
	if len(records) == 0 || head == nil {
		return ledgerCheckpointPayload{}, nil, nil, fmt.Errorf("action ledger checkpoint requires a retained chain")
	}
	statuses, err := BuildCallStatuses(records)
	if err != nil {
		return ledgerCheckpointPayload{}, nil, nil, err
	}
	active := make(map[string]bool, len(statuses))
	terminal := make([]CallStatus, 0, len(statuses))
	terminalCallIDs := make(map[string]struct{}, len(statuses))
	for _, status := range statuses {
		if status.TerminalComplete {
			terminal = append(terminal, status)
			terminalCallIDs[status.CallID] = struct{}{}
		} else {
			active[status.CallID] = true
		}
	}
	activeRecords := make([]Record, 0, len(records))
	for _, record := range records {
		if active[record.Call.CallID] {
			activeRecords = append(activeRecords, record)
		}
	}
	sort.Slice(terminal, func(i, j int) bool { return terminal[i].LastSequence < terminal[j].LastSequence })
	terminalDigest := ""
	for _, status := range terminal {
		terminalDigest = advanceTerminalDigest(terminalDigest, status.CallID)
	}
	return ledgerCheckpointPayload{
		Schema: checkpointSchema, RepositoryIdentity: s.storage.RepositoryIdentity(),
		Head: *head, LastRecord: records[len(records)-1],
		ActiveRecords: activeRecords, TerminalCallCount: uint64(len(terminal)),
		TerminalCallDigest: terminalDigest,
	}, statuses, terminalCallIDs, nil
}

func (s *Store) advanceCheckpoint(
	current ledgerCheckpointPayload,
	terminalCallIDs map[string]struct{},
	sealed Record,
) (ledgerCheckpointPayload, []CallStatus, string, error) {
	if _, terminal := terminalCallIDs[sealed.Call.CallID]; terminal {
		return ledgerCheckpointPayload{}, nil, "", fmt.Errorf("action ledger call %s has an event after its terminal event", sealed.Call.CallID)
	}
	nextHead, err := advanceChainHead(current.Head, sealed)
	if err != nil {
		return ledgerCheckpointPayload{}, nil, "", err
	}
	activeRecords := append(append([]Record(nil), current.ActiveRecords...), sealed)
	statuses, err := BuildCallStatuses(activeRecords)
	if err != nil {
		return ledgerCheckpointPayload{}, nil, "", err
	}
	completedCallID := ""
	for _, status := range statuses {
		if status.CallID != sealed.Call.CallID || !status.TerminalComplete {
			continue
		}
		filtered := activeRecords[:0]
		for _, record := range activeRecords {
			if record.Call.CallID != status.CallID {
				filtered = append(filtered, record)
			}
		}
		activeRecords = filtered
		current.TerminalCallCount++
		current.TerminalCallDigest = advanceTerminalDigest(current.TerminalCallDigest, status.CallID)
		completedCallID = status.CallID
		break
	}
	current.Head = nextHead
	current.LastRecord = sealed
	current.ActiveRecords = activeRecords
	current.Files = nil
	return current, statuses, completedCallID, nil
}

func advanceTerminalDigest(previous, callID string) string {
	digest := sha256.New()
	_, _ = digest.Write([]byte(previous))
	_, _ = digest.Write([]byte{0})
	_, _ = digest.Write([]byte(callID))
	return hex.EncodeToString(digest.Sum(nil))
}

func (s *Store) fastCheckpointLocked() (ledgerCheckpointPayload, map[string]struct{}, bool) {
	if s.checkpoint == nil {
		return ledgerCheckpointPayload{}, nil, false
	}
	if err := s.storage.ValidateJSONLFile(s.checkpointPath, maxCheckpointBytes); err != nil {
		return ledgerCheckpointPayload{}, nil, false
	}
	generation, err := ledgerFileGeneration(s.checkpointPath)
	if err != nil || generation != s.checkpoint.checkpointGeneration {
		return ledgerCheckpointPayload{}, nil, false
	}
	currentFiles, err := s.currentLedgerFileCheckpoints()
	if err != nil || len(currentFiles) != len(s.checkpoint.payload.Files) {
		return ledgerCheckpointPayload{}, nil, false
	}
	for index, expected := range s.checkpoint.payload.Files {
		if currentFiles[index] != expected {
			return ledgerCheckpointPayload{}, nil, false
		}
	}
	payload := s.checkpoint.payload
	payload.Files = append([]ledgerFileCheckpoint(nil), payload.Files...)
	payload.ActiveRecords = append([]Record(nil), payload.ActiveRecords...)
	return payload, s.checkpoint.terminalCallIDs, true
}

func (s *Store) publishCheckpointLocked(
	payload ledgerCheckpointPayload,
	terminalCallIDs map[string]struct{},
	completedCallID string,
) error {
	files, err := s.currentLedgerFileCheckpoints()
	if err != nil {
		return err
	}
	payload.Files = files
	body, err := s.encodeCheckpoint(payload)
	if err != nil {
		return err
	}
	if err := s.storage.PublishPrivateFile(checkpointFileName, body); err != nil {
		return fmt.Errorf("publish action ledger checkpoint: %w", err)
	}
	generation, err := ledgerFileGeneration(s.checkpointPath)
	if err != nil {
		return fmt.Errorf("observe action ledger checkpoint generation: %w", err)
	}
	if terminalCallIDs == nil {
		terminalCallIDs = make(map[string]struct{})
	}
	if completedCallID != "" {
		terminalCallIDs[completedCallID] = struct{}{}
	}
	s.checkpoint = &ledgerCheckpointCache{
		payload: payload, checkpointGeneration: generation, terminalCallIDs: terminalCallIDs,
	}
	return nil
}

func (s *Store) currentLedgerFileCheckpoints() ([]ledgerFileCheckpoint, error) {
	paths, err := jsonl.PathsOldestFirst(s.livePath, s.policy.MaxArchives)
	if err != nil {
		return nil, err
	}
	paths = append(paths, s.headPath)
	files := make([]ledgerFileCheckpoint, 0, len(paths))
	for _, path := range paths {
		name := filepath.Base(path)
		checkpoint, err := s.fileCheckpoint(path, name)
		if err != nil {
			return nil, err
		}
		files = append(files, checkpoint)
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Name < files[j].Name })
	return files, nil
}

func (s *Store) fileCheckpoint(path, name string) (ledgerFileCheckpoint, error) {
	maximum := s.policy.MaxBytes
	if name == headFileName {
		maximum = maxHeadBytes
	}
	if err := s.storage.ValidateJSONLFile(path, maximum); err != nil {
		return ledgerFileCheckpoint{}, err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return ledgerFileCheckpoint{}, err
	}
	generation, err := ledgerFileGeneration(path)
	if err != nil {
		return ledgerFileCheckpoint{}, err
	}
	return ledgerFileCheckpoint{Name: name, Generation: generation, Size: info.Size()}, nil
}

func (s *Store) encodeCheckpoint(payload ledgerCheckpointPayload) ([]byte, error) {
	payload.Schema = checkpointSchema
	payload.RepositoryIdentity = s.storage.RepositoryIdentity()
	var identity string
	if err := s.storage.WithIdentity(func(key *actionstate.IdentityKey) error {
		payload.KeyID = key.ID()
		body, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		identity = key.Identity(actionstate.DomainLedger, []byte(checkpointSchema), body)
		return nil
	}); err != nil {
		return nil, err
	}
	body, err := json.Marshal(ledgerCheckpointEnvelope{Payload: payload, Identity: identity})
	if err != nil {
		return nil, err
	}
	body = append(body, '\n')
	if len(body) > maxCheckpointBytes {
		return nil, fmt.Errorf("action ledger checkpoint exceeds %d bytes", maxCheckpointBytes)
	}
	return body, nil
}

func (s *Store) decodeCheckpoint(body []byte) (ledgerCheckpointPayload, error) {
	if len(body) == 0 || len(body) > maxCheckpointBytes {
		return ledgerCheckpointPayload{}, fmt.Errorf("action ledger checkpoint size is invalid")
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	var envelope ledgerCheckpointEnvelope
	if err := decoder.Decode(&envelope); err != nil {
		return ledgerCheckpointPayload{}, err
	}
	var trailing json.RawMessage
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return ledgerCheckpointPayload{}, fmt.Errorf("action ledger checkpoint contains trailing data")
	}
	payloadBody, err := json.Marshal(envelope.Payload)
	if err != nil {
		return ledgerCheckpointPayload{}, err
	}
	if err := s.storage.WithIdentity(func(key *actionstate.IdentityKey) error {
		want := key.Identity(actionstate.DomainLedger, []byte(checkpointSchema), payloadBody)
		if envelope.Payload.KeyID != key.ID() || envelope.Identity != want {
			return fmt.Errorf("action ledger checkpoint authentication failed")
		}
		return nil
	}); err != nil {
		return ledgerCheckpointPayload{}, err
	}
	if envelope.Payload.Schema != checkpointSchema ||
		envelope.Payload.RepositoryIdentity != s.storage.RepositoryIdentity() ||
		envelope.Payload.Head.validate() != nil ||
		envelope.Payload.LastRecord.Sequence != envelope.Payload.Head.LastSequence ||
		envelope.Payload.LastRecord.Digest != envelope.Payload.Head.LastDigest {
		return ledgerCheckpointPayload{}, fmt.Errorf("action ledger checkpoint binding is invalid")
	}
	if err := s.validateCheckpointPayload(envelope.Payload); err != nil {
		return ledgerCheckpointPayload{}, err
	}
	canonical, err := s.encodeCheckpoint(envelope.Payload)
	if err != nil || !bytes.Equal(body, canonical) {
		return ledgerCheckpointPayload{}, fmt.Errorf("action ledger checkpoint is not canonical")
	}
	return envelope.Payload, nil
}

func (s *Store) validateCheckpointPayload(payload ledgerCheckpointPayload) error {
	if payload.TerminalCallCount > payload.Head.EntryCount ||
		payload.TerminalCallCount == 0 && payload.TerminalCallDigest != "" ||
		payload.TerminalCallCount > 0 && !validCheckpointDigest(payload.TerminalCallDigest) {
		return fmt.Errorf("action ledger checkpoint terminal summary is invalid")
	}
	for index, file := range payload.Files {
		if file.Generation == "" || file.Size < 0 ||
			index > 0 && payload.Files[index-1].Name >= file.Name ||
			!allowedLedgerEntry(file.Name, false, s.policy.MaxArchives) || file.Name == checkpointFileName {
			return fmt.Errorf("action ledger checkpoint file summary is invalid")
		}
	}
	statuses, err := BuildCallStatuses(payload.ActiveRecords)
	if err != nil {
		return fmt.Errorf("action ledger checkpoint active-call summary is invalid: %w", err)
	}
	for _, status := range statuses {
		if status.TerminalComplete {
			return fmt.Errorf("action ledger checkpoint retains a terminal call")
		}
	}
	for index, record := range payload.ActiveRecords {
		if record.Call.RepositoryIdentity != s.storage.RepositoryIdentity() ||
			record.Sequence > payload.Head.LastSequence ||
			index > 0 && payload.ActiveRecords[index-1].Sequence >= record.Sequence {
			return fmt.Errorf("action ledger checkpoint active record binding is invalid")
		}
		if err := s.validateRecordKeyGeneration(record); err != nil {
			return fmt.Errorf("action ledger checkpoint identity generation is invalid: %w", err)
		}
	}
	return nil
}

func validCheckpointDigest(value string) bool {
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == sha256.Size
}
