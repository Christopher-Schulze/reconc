package actionapproval

import (
	"bytes"
	"testing"
)

func BenchmarkApprovalReceiptVerification(b *testing.B) {
	request, registry, privateKey, now := testApprovalFixture(b)
	_, receipt, err := SignReceipt(
		request, "security-primary", privateKey, DecisionApprove, now,
		bytes.NewReader(bytes.Repeat([]byte{0x42}, 16)),
	)
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for iteration := 0; iteration < b.N; iteration++ {
		if _, err := VerifyReceipt(registry, request, receipt, now); err != nil {
			b.Fatal(err)
		}
	}
}
