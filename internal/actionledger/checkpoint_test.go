package actionledger

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"testing"

	"reconc.dev/reconc/internal/action"
	"reconc.dev/reconc/internal/actionstate"
)

func TestCheckpointBudgetContinuationPreservesTerminalBoundaries(t *testing.T) {
	for _, cold := range []bool{false, true} {
		t.Run(fmt.Sprint(cold), func(t *testing.T) {
			fixture := newLedgerStoreFixture(t)
			fixture.append(t, EventRequestAccepted)
			decision := testLedgerRecord(EventPreDecision)
			decision.Decision.Decision = action.DecisionBlock
			decision.Decision.Reason = action.ReasonRuleMatched
			decision.PreDecision.Outcome = action.OutcomeDispatchBlocked
			fixture.appendRecord(t, decision)
			appendTerminalBenchmarkCall(t, fixture, 7)
			if cold {
				var err error
				fixture.store, err = OpenStore(fixture.storage)
				if err != nil {
					t.Fatal(err)
				}
			}
			reserved := budgetLifecycleTransition(BudgetReserved)
			reserved.Decision = decision.Decision
			fixture.appendRecord(t, reserved)
			denied := budgetLifecycleTransition(BudgetDenied)
			fixture.appendRecord(t, denied)
			before, err := fixture.store.Verify(context.Background())
			if err != nil || !before.CallsComplete {
				t.Fatalf("completed denial: %+v, %v", before, err)
			}
			for _, invalid := range []Record{reserved, denied} {
				invalid.Timestamp = "2026-08-11T12:00:01Z"
				if _, err := fixture.store.Append(context.Background(), fixture.bindRecord(invalid)); err == nil {
					t.Fatal("completed denial accepted another budget transition")
				}
			}
			failureBudget := fixture.bindRecord(reserved)
			bindBenchmarkCall(fixture, &failureBudget, 7)
			if _, err := fixture.store.Append(context.Background(), failureBudget); err == nil {
				t.Fatal("terminal failure accepted a budget continuation")
			}
			after, err := fixture.store.Verify(context.Background())
			if err != nil || after.RecordCount != before.RecordCount || !after.CallsComplete {
				t.Fatalf("rejected continuation changed ledger: %+v, %v", after, err)
			}
		})
	}
}

func TestIncrementalCheckpointRejectsHistoricalTamperWithRestoredMtime(t *testing.T) {
	fixture := newLedgerStoreFixture(t)
	appendTerminalBenchmarkCall(t, fixture, 1)
	before, err := os.ReadFile(fixture.store.livePath)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(fixture.store.livePath)
	if err != nil {
		t.Fatal(err)
	}
	tampered := bytes.Replace(before, []byte(`"principal":"release-operator"`), []byte(`"principal":"hostile-operator"`), 1)
	if bytes.Equal(tampered, before) {
		t.Fatal("test fixture did not contain the principal field")
	}
	if err := os.WriteFile(fixture.store.livePath, tampered, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(fixture.store.livePath, info.ModTime(), info.ModTime()); err != nil {
		t.Fatal(err)
	}
	record := fixture.record(EventRequestAccepted)
	bindBenchmarkCall(fixture, &record, 2)
	if _, err := fixture.store.Append(context.Background(), record); err == nil || ErrorCode(err) != "ledger_corrupt" {
		t.Fatalf("append after restored-mtime tamper = %v", err)
	}
}

func TestIncrementalCheckpointRecoversCorruptionAndExternalWriter(t *testing.T) {
	fixture := newLedgerStoreFixture(t)
	appendTerminalBenchmarkCall(t, fixture, 1)
	if err := os.WriteFile(fixture.store.checkpointPath, []byte("corrupt\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cold, err := OpenStore(fixture.storage)
	if err != nil {
		t.Fatal(err)
	}
	coldFixture := *fixture
	coldFixture.store = cold
	appendTerminalBenchmarkCall(t, &coldFixture, 2)
	body, err := fixture.storage.ReadPrivateFile(checkpointFileName, maxCheckpointBytes)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.store.decodeCheckpoint(body); err != nil {
		t.Fatalf("rebuilt checkpoint: %v", err)
	}

	external, err := OpenStore(fixture.storage)
	if err != nil {
		t.Fatal(err)
	}
	record := fixture.record(EventRequestAccepted)
	bindBenchmarkCall(fixture, &record, 3)
	if _, err := external.Append(context.Background(), record); err != nil {
		t.Fatal(err)
	}
	failure := fixture.record(EventTerminalFailure)
	bindBenchmarkCall(fixture, &failure, 3)
	if _, err := fixture.store.Append(context.Background(), failure); err != nil {
		t.Fatalf("append after external writer: %v", err)
	}
}

func TestIncrementalCheckpointRecoversMissingCheckpointOnColdAppend(t *testing.T) {
	fixture := newLedgerStoreFixture(t)
	appendTerminalBenchmarkCall(t, fixture, 1)
	if err := os.Remove(fixture.store.checkpointPath); err != nil {
		t.Fatal(err)
	}
	cold, err := OpenStore(fixture.storage)
	if err != nil {
		t.Fatal(err)
	}
	coldFixture := *fixture
	coldFixture.store = cold
	appendTerminalBenchmarkCall(t, &coldFixture, 2)
	body, err := fixture.storage.ReadPrivateFile(checkpointFileName, maxCheckpointBytes)
	if err != nil {
		t.Fatal(err)
	}
	checkpoint, err := cold.decodeCheckpoint(body)
	if err != nil {
		t.Fatal(err)
	}
	if checkpoint.TerminalCallCount != 2 || checkpoint.TerminalCallDigest == "" {
		t.Fatalf("rebuilt checkpoint summary = %#v", checkpoint)
	}
}

func TestIncrementalCheckpointAuthenticationAndTerminalIndex(t *testing.T) {
	fixture := newLedgerStoreFixture(t)
	appendTerminalBenchmarkCall(t, fixture, 1)
	body, err := fixture.storage.ReadPrivateFile(checkpointFileName, maxCheckpointBytes)
	if err != nil {
		t.Fatal(err)
	}
	var envelope ledgerCheckpointEnvelope
	if err := json.Unmarshal(body, &envelope); err != nil {
		t.Fatal(err)
	}
	envelope.Payload.TerminalCallCount++
	tampered, err := json.Marshal(envelope)
	if err != nil {
		t.Fatal(err)
	}
	tampered = append(tampered, '\n')
	if _, err := fixture.store.decodeCheckpoint(tampered); err == nil {
		t.Fatal("unauthenticated checkpoint payload was accepted")
	}
	if err := os.WriteFile(fixture.store.checkpointPath, tampered, 0o600); err != nil {
		t.Fatal(err)
	}
	appendTerminalBenchmarkCall(t, fixture, 2)

	reused := fixture.record(EventRequestAccepted)
	bindBenchmarkCall(fixture, &reused, 1)
	if _, err := fixture.store.Append(context.Background(), reused); err == nil || ErrorCode(err) != "ledger_corrupt" {
		t.Fatalf("event after terminal call = %v", err)
	}

	body, err = fixture.storage.ReadPrivateFile(checkpointFileName, maxCheckpointBytes)
	if err != nil {
		t.Fatal(err)
	}
	checkpoint, err := fixture.store.decodeCheckpoint(body)
	if err != nil {
		t.Fatal(err)
	}
	if checkpoint.TerminalCallCount != 2 || checkpoint.TerminalCallDigest == "" || len(checkpoint.ActiveRecords) != 0 {
		t.Fatalf("checkpoint lifecycle summary = %#v", checkpoint)
	}
}

func TestIncrementalCheckpointCannotOutliveRetainedChain(t *testing.T) {
	fixture := newLedgerStoreFixture(t)
	appendTerminalBenchmarkCall(t, fixture, 1)
	for _, path := range []string{fixture.store.livePath, fixture.store.headPath} {
		if err := os.Remove(path); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := fixture.store.Verify(context.Background()); err == nil {
		t.Fatalf("orphan checkpoint verification = %v", err)
	}
}

func appendTerminalBenchmarkCall(t testing.TB, fixture *ledgerStoreFixture, index int) {
	t.Helper()
	accepted := fixture.record(EventRequestAccepted)
	bindBenchmarkCall(fixture, &accepted, index)
	fixture.appendRecord(t, accepted)
	failure := fixture.record(EventTerminalFailure)
	bindBenchmarkCall(fixture, &failure, index)
	fixture.appendRecord(t, failure)
}

func bindBenchmarkCall(fixture *ledgerStoreFixture, record *Record, index int) {
	record.Call.CallID = benchmarkCallID(index)
	record.Call.RequestIdentity = fixture.identity("request-" + benchmarkCallID(index))
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
