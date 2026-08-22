package actionledger

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
)

func BenchmarkLedgerAppend(b *testing.B) {
	fixture := newLedgerStoreFixture(b)
	b.ReportAllocs()
	b.ResetTimer()
	for iteration := 0; iteration < b.N; iteration++ {
		record := fixture.record(EventRequestAccepted)
		record.Call.CallID = benchmarkCallID(iteration)
		record.Call.RequestIdentity = fixture.identity(fmt.Sprintf("request-%d", iteration))
		if _, err := fixture.store.Append(context.Background(), record); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkLedgerAppendWithTerminalHistory(b *testing.B) {
	fixture := newLedgerStoreFixture(b)
	for index := 0; index < 256; index++ {
		appendTerminalBenchmarkCall(b, fixture, index+1)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for iteration := 0; iteration < b.N; iteration++ {
		appendTerminalBenchmarkCall(b, fixture, 257+iteration)
	}
}

func BenchmarkLedgerAppendRetentionScaling(b *testing.B) {
	for _, test := range []struct {
		name     string
		archives uint32
	}{
		{name: "empty", archives: 0},
		{name: "near_rotation", archives: 0},
		{name: "maximum_retained", archives: MaxArchives},
	} {
		b.Run(test.name, func(b *testing.B) {
			fixture := newLedgerStoreFixture(b)
			start := 1
			if test.name != "empty" {
				fixture.store.policy.MaxBytes = 8 << 10
				start = prepareRetainedLedgerBenchmark(b, fixture, test.archives)
			}
			b.ReportAllocs()
			b.ResetTimer()
			for iteration := 0; iteration < b.N; iteration++ {
				appendTerminalBenchmarkCall(b, fixture, start+iteration)
			}
		})
	}
}

func prepareRetainedLedgerBenchmark(
	b *testing.B,
	fixture *ledgerStoreFixture,
	targetArchives uint32,
) int {
	b.Helper()
	for index := 1; index <= 4096; index++ {
		appendTerminalBenchmarkCall(b, fixture, index)
		info, err := os.Stat(fixture.store.livePath)
		if err != nil {
			b.Fatal(err)
		}
		archives := fixture.store.archiveCount()
		if archives == targetArchives && info.Size() >= fixture.store.policy.MaxBytes/2 {
			return index + 1
		}
	}
	b.Fatalf("retained ledger did not reach %d archives", targetArchives)
	return 0
}

func BenchmarkLedgerCheckpointAdvanceNoActiveCalls(b *testing.B) {
	store, checkpoint, terminalCallIDs, sealed := checkpointAdvanceBenchmarkFixture(b, 0)
	b.ReportAllocs()
	b.ResetTimer()
	for iteration := 0; iteration < b.N; iteration++ {
		if _, _, _, err := store.advanceCheckpoint(checkpoint, terminalCallIDs, sealed); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkLedgerCheckpointAdvanceActive256(b *testing.B) {
	store, checkpoint, terminalCallIDs, sealed := checkpointAdvanceBenchmarkFixture(b, 256)
	b.ReportAllocs()
	b.ResetTimer()
	for iteration := 0; iteration < b.N; iteration++ {
		if _, _, _, err := store.advanceCheckpoint(checkpoint, terminalCallIDs, sealed); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkLedgerCheckpointAdvanceTerminal65536(b *testing.B) {
	store, checkpoint, terminalCallIDs, sealed := checkpointAdvanceBenchmarkFixture(b, 0)
	for index := 0; index < 65536; index++ {
		terminalCallIDs[benchmarkCallID(index+1000)] = struct{}{}
	}
	b.ReportAllocs()
	b.ResetTimer()
	for iteration := 0; iteration < b.N; iteration++ {
		if _, _, _, err := store.advanceCheckpoint(checkpoint, terminalCallIDs, sealed); err != nil {
			b.Fatal(err)
		}
	}
}

func checkpointAdvanceBenchmarkFixture(
	b *testing.B,
	activeCalls int,
) (*Store, ledgerCheckpointPayload, map[string]struct{}, Record) {
	b.Helper()
	fixture := newLedgerStoreFixture(b)
	records := make([]Record, 0, activeCalls+2)
	var head chainHead
	appendRecord := func(record Record) {
		sequence := uint64(len(records) + 1)
		previous := ""
		if sequence > 1 {
			previous = records[len(records)-1].Digest
		}
		sealed, _, err := Seal(record, sequence, previous)
		if err != nil {
			b.Fatal(err)
		}
		if sequence == 1 {
			head = newChainHead(sealed)
		} else {
			head, err = advanceChainHead(head, sealed)
			if err != nil {
				b.Fatal(err)
			}
		}
		records = append(records, sealed)
	}
	if activeCalls == 0 {
		accepted := fixture.record(EventRequestAccepted)
		bindBenchmarkCall(fixture, &accepted, 1)
		appendRecord(accepted)
		failure := fixture.record(EventTerminalFailure)
		bindBenchmarkCall(fixture, &failure, 1)
		appendRecord(failure)
	} else {
		for index := 0; index < activeCalls; index++ {
			accepted := fixture.record(EventRequestAccepted)
			bindBenchmarkCall(fixture, &accepted, index+1)
			appendRecord(accepted)
		}
	}
	checkpoint, _, terminalCallIDs, err := fixture.store.checkpointFromRecords(records, &head)
	if err != nil {
		b.Fatal(err)
	}
	candidate := fixture.record(EventRequestAccepted)
	bindBenchmarkCall(fixture, &candidate, activeCalls+70000)
	sealed, _, err := Seal(candidate, head.LastSequence+1, head.LastDigest)
	if err != nil {
		b.Fatal(err)
	}
	return fixture.store, checkpoint, terminalCallIDs, sealed
}

func benchmarkCallID(value int) string {
	encoded := []byte(strings.Repeat("a", 26))
	for index := len(encoded) - 1; value > 0; index-- {
		encoded[index] = byte('a' + value%26)
		value /= 26
	}
	return "act_" + string(encoded)
}
