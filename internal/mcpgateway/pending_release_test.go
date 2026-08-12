package mcpgateway

import (
	"bytes"
	"encoding/json"
	"strconv"
	"testing"
)

func TestPendingApprovalReleaseZeroesOwnedRawBuffers(t *testing.T) {
	originalRPCID := json.RawMessage(`"rpc-secret"`)
	originalParams := json.RawMessage(`{"secret":"params"}`)
	canonicalArguments := json.RawMessage(`{"secret":"arguments"}`)
	rawResult := json.RawMessage(`{"secret":"result"}`)
	pending := pendingApproval{
		originalRPCID:      originalRPCID,
		originalParams:     originalParams,
		canonicalArguments: canonicalArguments,
		rawResult:          rawResult,
		repositoryPaths:    []RepositoryPathBinding{{Lexical: "/path", Identity: "identity"}},
	}

	pending.release()

	for _, test := range []struct {
		name  string
		value []byte
	}{
		{name: "original RPC ID", value: originalRPCID},
		{name: "original parameters", value: originalParams},
		{name: "canonical arguments", value: canonicalArguments},
		{name: "raw result", value: rawResult},
	} {
		t.Run(test.name, func(t *testing.T) {
			if !bytes.Equal(test.value, make([]byte, len(test.value))) {
				t.Fatalf("released buffer still contains data: %q", test.value)
			}
		})
	}
	if pending.originalRPCID != nil || pending.originalParams != nil ||
		pending.canonicalArguments != nil || pending.rawResult != nil ||
		pending.repositoryPaths != nil || pending.callID != "" || pending.ledger != nil ||
		pending.snapshot.Plan != nil || pending.preRequest.Arguments != nil ||
		pending.evaluation.Request.Arguments != nil {
		t.Fatalf("released pending approval retains references: %#v", pending)
	}
}

func TestPendingApprovalStorageReleasesRemovedAndRejectedBuffers(t *testing.T) {
	for _, test := range []struct {
		name    string
		setup   func(*Gateway)
		state   string
		wantErr bool
	}{
		{name: "removed", state: "stored"},
		{name: "empty state rejected", state: "", wantErr: true},
		{
			name: "duplicate rejected",
			setup: func(gateway *Gateway) {
				gateway.pending["duplicate"] = pendingApproval{}
			},
			state:   "duplicate",
			wantErr: true,
		},
		{
			name: "capacity rejected",
			setup: func(gateway *Gateway) {
				for index := 0; index < MaxPendingApprovals; index++ {
					gateway.pending[strconv.Itoa(index)] = pendingApproval{}
				}
			},
			state:   "overflow",
			wantErr: true,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			gateway := &Gateway{pending: make(map[string]pendingApproval)}
			if test.setup != nil {
				test.setup(gateway)
			}
			owned := json.RawMessage(`{"sensitive":true}`)
			err := gateway.storePending(test.state, pendingApproval{rawResult: owned})
			if !test.wantErr {
				if err != nil {
					t.Fatal(err)
				}
				gateway.removePending(test.state)
			} else if err == nil {
				t.Fatal("rejected pending approval was accepted")
			}
			if !bytes.Equal(owned, make([]byte, len(owned))) {
				t.Fatalf("pending storage lifecycle retained raw bytes: %q", owned)
			}
		})
	}
}
