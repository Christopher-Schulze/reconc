package actionledger

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"testing"
)

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
	appendTerminalBenchmarkCall(t, fixture, 2)
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
