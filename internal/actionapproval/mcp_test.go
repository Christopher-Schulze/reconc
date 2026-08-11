package actionapproval

import (
	"bytes"
	"encoding/base64"
	"errors"
	"strconv"
	"testing"

	"reconc.dev/reconc/internal/action"
)

func TestMCPApprovalInputRequiredAndRetryRoundTrip(t *testing.T) {
	request, _, privateKey, now := testApprovalFixture(t)
	state := base64.RawURLEncoding.EncodeToString([]byte("sealed-approval-state"))
	result, err := BuildMCPInputRequired(request, state)
	if err != nil {
		t.Fatal(err)
	}
	body, err := EncodeMCPInputRequired(result)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(body, []byte(`"resultType":"input_required"`)) ||
		!bytes.Contains(body, []byte(`"method":"elicitation/create"`)) ||
		bytes.Contains(body, []byte("credential-secret")) {
		t.Fatalf("MCP input-required result is invalid or private: %s", body)
	}
	_, receipt, err := SignReceipt(
		request, "security-primary", privateKey, DecisionApprove,
		now,
		bytes.NewReader(bytes.Repeat([]byte{0x61}, 16)),
	)
	if err != nil {
		t.Fatal(err)
	}
	original := []byte(`{"arguments":{"target":"staging"},"name":"execute"}`)
	retry := []byte(`{"arguments":{"target":"staging"},"inputResponses":{"reconc_approval":{"action":"accept","content":{"receipt":` +
		strconv.Quote(string(receipt)) + `}}},"name":"execute","requestState":` + strconv.Quote(state) + `}`)
	got, err := ParseMCPApprovalRetry([]byte(`1`), []byte(`2`), original, retry, state)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, receipt) {
		t.Fatal("MCP retry changed the signed receipt bytes")
	}
	got[0] = 'x'
	if receipt[0] != '{' {
		t.Fatal("MCP retry returned an aliased receipt buffer")
	}
}

func TestMCPApprovalRetryRejectsEveryProtocolDrift(t *testing.T) {
	state := base64.RawURLEncoding.EncodeToString([]byte("sealed-approval-state"))
	original := `{"arguments":{"target":"staging"},"name":"execute"}`
	accepted := `{"arguments":{"target":"staging"},"inputResponses":{"reconc_approval":{"action":"accept","content":{"receipt":"signed"}}},"name":"execute","requestState":` + strconv.Quote(state) + `}`
	tests := []struct {
		name       string
		originalID string
		retryID    string
		original   string
		retry      string
		want       action.ReasonCode
	}{
		{name: "same id", originalID: `1`, retryID: `1`, original: original, retry: accepted, want: action.ReasonProtocolError},
		{name: "invalid id", originalID: `true`, retryID: `2`, original: original, retry: accepted, want: action.ReasonProtocolError},
		{name: "changed argument", originalID: `1`, retryID: `2`, original: original,
			retry: bytesReplace(accepted, `"staging"`, `"production"`), want: action.ReasonApprovalInvalid},
		{name: "changed state", originalID: `1`, retryID: `2`, original: original,
			retry: bytesReplace(accepted, strconv.Quote(state), `"c3RhbGUtYXBwcm92YWw"`), want: action.ReasonApprovalInvalid},
		{name: "unexpected response", originalID: `1`, retryID: `2`, original: original,
			retry: bytesReplace(accepted, `"reconc_approval"`, `"other"`), want: action.ReasonApprovalInvalid},
		{name: "unsigned accept", originalID: `1`, retryID: `2`, original: original,
			retry: bytesReplace(accepted, `"signed"`, `""`), want: action.ReasonApprovalInvalid},
		{name: "declined", originalID: `1`, retryID: `2`, original: original,
			retry: `{"arguments":{"target":"staging"},"inputResponses":{"reconc_approval":{"action":"decline"}},"name":"execute","requestState":` + strconv.Quote(state) + `}`,
			want:  action.ReasonApprovalRejected},
		{name: "cancelled", originalID: `1`, retryID: `2`, original: original,
			retry: `{"arguments":{"target":"staging"},"inputResponses":{"reconc_approval":{"action":"cancel"}},"name":"execute","requestState":` + strconv.Quote(state) + `}`,
			want:  action.ReasonCancelled},
		{name: "duplicate field", originalID: `1`, retryID: `2`, original: original,
			retry: bytesReplace(accepted, `"name":"execute"`, `"name":"execute","name":"execute"`), want: action.ReasonProtocolError},
		{name: "original already retried", originalID: `1`, retryID: `2`, original: accepted,
			retry: accepted, want: action.ReasonProtocolError},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := ParseMCPApprovalRetry(
				[]byte(test.originalID), []byte(test.retryID),
				[]byte(test.original), []byte(test.retry), state,
			)
			requireApprovalCode(t, err, test.want)
		})
	}
}

func TestMCPApprovalResultRejectsInvalidConstructionAndUnsupportedClientIsBounded(t *testing.T) {
	request, _, _, _ := testApprovalFixture(t)
	state := base64.RawURLEncoding.EncodeToString([]byte("sealed-approval-state"))
	result, err := BuildMCPInputRequired(request, state)
	if err != nil {
		t.Fatal(err)
	}
	result.InputRequests[MCPApprovalInputID] = MCPElicitRequest{Method: "elicitation/create"}
	if _, err := EncodeMCPInputRequired(result); err == nil {
		t.Fatal("invalid MCP elicitation result was encoded")
	}
	if _, err := BuildMCPInputRequired(request, "not base64url!"); err == nil {
		t.Fatal("unsealed MCP request state was accepted")
	}
	failure, err := UnsupportedMCPApproval(request.CallID)
	if err != nil {
		t.Fatal(err)
	}
	if failure.Outcome != "approval_required" || failure.ReasonCode != action.ReasonApprovalRequired ||
		failure.DispatchStatus != "not_dispatched" || failure.DeliveryStatus != "blocked" {
		t.Fatalf("unsupported MCP result = %#v", failure)
	}
}

func bytesReplace(value, old, replacement string) string {
	return string(bytes.Replace([]byte(value), []byte(old), []byte(replacement), 1))
}

func requireApprovalCode(t testing.TB, err error, code action.ReasonCode) {
	t.Helper()
	var approvalErr *ApprovalError
	if !errors.As(err, &approvalErr) || approvalErr.Code != code {
		t.Fatalf("approval error = %v, want %s", err, code)
	}
}
