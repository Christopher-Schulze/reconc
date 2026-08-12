package actionapproval

import (
	"bytes"
	"encoding/base64"
	"testing"
)

func FuzzDecodeApprovalRequest(f *testing.F) {
	request, _, _, _ := testApprovalFixture(f)
	body, err := EncodeRequest(request)
	if err != nil {
		f.Fatal(err)
	}
	f.Add(body)
	f.Add([]byte(`{"schema":"reconc.action-approval-request/v1"}`))
	f.Fuzz(func(t *testing.T, input []byte) {
		decoded, err := DecodeRequest(input)
		if err != nil {
			return
		}
		encoded, err := EncodeRequest(decoded)
		if err != nil || !bytes.Equal(encoded, input) {
			t.Fatalf("accepted approval request did not round-trip exactly: %v", err)
		}
	})
}

func FuzzDecodeApprovalReceipt(f *testing.F) {
	request, _, privateKey, now := testApprovalFixture(f)
	_, body, err := SignReceipt(
		request, "security-primary", privateKey, DecisionApprove,
		now,
		bytes.NewReader(bytes.Repeat([]byte{0x45}, 16)),
	)
	if err != nil {
		f.Fatal(err)
	}
	f.Add(body)
	f.Add([]byte(`{}`))
	f.Fuzz(func(t *testing.T, input []byte) {
		decoded, err := DecodeReceipt(input)
		if err != nil {
			return
		}
		encoded, err := EncodeReceipt(decoded)
		if err != nil || !bytes.Equal(encoded, input) {
			t.Fatalf("accepted approval receipt did not round-trip exactly: %v", err)
		}
	})
}

func FuzzDecodeApprovalRegistry(f *testing.F) {
	_, registry, _, _ := testApprovalFixture(f)
	body, err := canonicalJSON(registry.registry)
	if err != nil {
		f.Fatal(err)
	}
	f.Add(body)
	f.Add([]byte(`{"authorities":[]}`))
	f.Fuzz(func(t *testing.T, input []byte) {
		decoded, err := DecodeRegistry(input)
		if err == nil && decoded.Identity() == "" {
			t.Fatal("accepted approval registry lacks an identity")
		}
	})
}

func FuzzParseMCPApprovalRetry(f *testing.F) {
	state := base64.RawURLEncoding.EncodeToString([]byte("sealed-approval-state"))
	original := []byte(`{"arguments":{"target":"staging"},"name":"execute"}`)
	f.Add([]byte(`{"arguments":{"target":"staging"},"inputResponses":{"reconc_approval":{"action":"accept","content":{"receipt":"signed"}}},"name":"execute","requestState":"c2VhbGVkLWFwcHJvdmFsLXN0YXRl"}`))
	f.Add([]byte(`{}`))
	f.Fuzz(func(t *testing.T, retry []byte) {
		_, _ = ParseMCPApprovalRetry([]byte(`1`), []byte(`2`), original, retry, state)
	})
}

func FuzzParseMCPElicitationResponse(f *testing.F) {
	f.Add([]byte(`{"action":"accept","content":{"receipt":"signed"}}`))
	f.Add([]byte(`{"action":"cancel"}`))
	f.Add([]byte(`{}`))
	f.Fuzz(func(t *testing.T, input []byte) {
		_, _ = ParseMCPElicitationResponse(input)
	})
}
