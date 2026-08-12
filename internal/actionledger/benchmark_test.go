package actionledger

import (
	"context"
	"fmt"
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

func benchmarkCallID(value int) string {
	encoded := []byte(strings.Repeat("a", 26))
	for index := len(encoded) - 1; value > 0; index-- {
		encoded[index] = byte('a' + value%26)
		value /= 26
	}
	return "act_" + string(encoded)
}
