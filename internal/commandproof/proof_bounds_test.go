package commandproof

import (
	"strings"
	"testing"
	"time"
)

func TestValidateAndStoreRejectOversizedCommandProof(t *testing.T) {
	repo := newProofRepo(t)
	snapshot, err := CaptureStagedClean(repo)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if _, err := StoreSuccess(snapshot, strings.Repeat("x", maxProofSize), "direct", now, now); err == nil {
		t.Fatal("oversized command proof must not be published")
	}
}

func TestBoundedCommandOutputCapsMemoryWithoutShortWrite(t *testing.T) {
	output := &boundedCommandOutput{limit: 4}
	data := []byte("abcdefgh")
	written, err := output.Write(data)
	if err != nil || written != len(data) {
		t.Fatalf("Write = %d, %v", written, err)
	}
	if output.String() != "abcd" || !output.overflow {
		t.Fatalf("bounded output = %q overflow=%v", output.String(), output.overflow)
	}
}
