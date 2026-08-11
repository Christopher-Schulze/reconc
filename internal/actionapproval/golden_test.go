package actionapproval

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
)

func TestApprovalCanonicalSigningGolden(t *testing.T) {
	request, _, privateKey, now := testApprovalFixture(t)
	requestBody, err := EncodeRequest(request)
	if err != nil {
		t.Fatal(err)
	}
	receipt, receiptBody, err := SignReceipt(
		request, "security-primary", privateKey, DecisionApprove,
		now,
		bytes.NewReader(bytes.Repeat([]byte{0x73}, 16)),
	)
	if err != nil {
		t.Fatal(err)
	}
	signingBytes, err := ReceiptSigningBytes(receipt)
	if err != nil {
		t.Fatal(err)
	}
	assertApprovalGolden(t, "approval-request.golden.json", requestBody)
	assertApprovalGolden(t, "approval-receipt.golden.json", receiptBody)
	signingDigest := sha256.Sum256(signingBytes)
	assertApprovalGolden(t, "approval-signing.golden.sha256", []byte(hex.EncodeToString(signingDigest[:])))
}

func assertApprovalGolden(t testing.TB, name string, got []byte) {
	t.Helper()
	want, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read approval golden %s: %v\ngot=%s", name, err, got)
	}
	want = bytes.TrimSuffix(want, []byte("\n"))
	if !bytes.Equal(got, want) {
		t.Fatalf("approval golden %s drifted\nwant=%s\ngot=%s", name, want, got)
	}
}
