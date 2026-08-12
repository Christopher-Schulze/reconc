package actionstate

import (
	"bytes"
	"context"
	"errors"
	"os"
	"testing"

	"reconc.dev/reconc/internal/action"
	"reconc.dev/reconc/internal/actionapproval"
)

func TestEvidenceSnapshotCryptographicallyReverifiesStoredApprovalReceipt(t *testing.T) {
	fixture := newStoreFixture(t, []action.Budget{storeBudget(
		"evidence-approval", action.BudgetLimits{CallCount: 2, ApprovalCount: 2}, action.BudgetResetNever,
	)})
	input, reserved := fixture.reserve(t, callID("e"))
	issued := issueFixtureApproval(t, fixture, input, reserved)
	if _, err := consumeFixtureApproval(t, fixture, issued, actionapproval.DecisionApprove); err != nil {
		t.Fatal(err)
	}
	status, receipts, persisted, err := fixture.store.evidenceSnapshot(context.Background(), issued.registry)
	if err != nil {
		t.Fatal(err)
	}
	if !persisted || !status.Complete || !receipts.Evaluated || !receipts.Complete ||
		receipts.Applicable != 1 || receipts.Verified != 1 || receipts.Unavailable != 0 || receipts.Invalid != 0 ||
		len(receipts.Records) != 1 || receipts.Records[0].Verification != ApprovalReceiptVerified {
		t.Fatalf("evidence snapshot = status %#v receipts %#v", status, receipts)
	}
	if receipts.Records[0].ReceiptIdentity == "" || receipts.Records[0].AuthorityKeyID != "security-primary" {
		t.Fatalf("receipt verification omitted safe provenance: %#v", receipts.Records[0])
	}

	_, unavailable, _, err := fixture.store.evidenceSnapshot(context.Background(), LoadedApprovalRegistry{})
	if err != nil {
		t.Fatal(err)
	}
	if unavailable.Complete || unavailable.Unavailable != 1 || unavailable.Records[0].Verification != ApprovalReceiptUnavailable {
		t.Fatalf("missing authority registry was not explicit: %#v", unavailable)
	}
}

func TestStoredReceiptMaterialRejectsPartialOrMismatchedMetadata(t *testing.T) {
	fixture, baseline := completePersistedState(t)
	tests := []struct {
		name   string
		mutate func(*ApprovalRecord)
	}{
		{name: "missing decision", mutate: func(record *ApprovalRecord) { record.ReceiptDecision = "" }},
		{name: "missing signature", mutate: func(record *ApprovalRecord) { record.ReceiptSignature = "" }},
		{name: "wrong decision", mutate: func(record *ApprovalRecord) { record.ReceiptDecision = actionapproval.DecisionReject }},
		{name: "invalid signature", mutate: func(record *ApprovalRecord) { record.ReceiptSignature = "invalid" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			state := cloneState(baseline)
			test.mutate(&state.Approvals[0])
			state.Digest, _ = fixture.store.stateDigest(state)
			if err := fixture.store.validateState(state, true); err == nil {
				t.Fatal("malformed stored receipt material was accepted")
			}
		})
	}
}

func TestApprovalReceiptEvidenceDetectsCryptographicTamperAndLegacyAbsence(t *testing.T) {
	fixture := newStoreFixture(t, []action.Budget{storeBudget(
		"receipt-tamper", action.BudgetLimits{CallCount: 2, ApprovalCount: 2}, action.BudgetResetNever,
	)})
	input, reserved := fixture.reserve(t, callID("t"))
	issued := issueFixtureApproval(t, fixture, input, reserved)
	if _, err := consumeFixtureApproval(t, fixture, issued, actionapproval.DecisionApprove); err != nil {
		t.Fatal(err)
	}
	var state State
	if err := fixture.store.withLock(context.Background(), func() error {
		var err error
		state, _, err = fixture.store.loadState()
		return err
	}); err != nil {
		t.Fatal(err)
	}

	tampered := cloneState(state)
	if tampered.Approvals[0].ReceiptSignature[0] == 'A' {
		tampered.Approvals[0].ReceiptSignature = "B" + tampered.Approvals[0].ReceiptSignature[1:]
	} else {
		tampered.Approvals[0].ReceiptSignature = "A" + tampered.Approvals[0].ReceiptSignature[1:]
	}
	report := verifyStoredApprovalReceipts(tampered.Approvals, issued.registry)
	if report.Complete || report.Invalid != 1 || report.Records[0].Verification != ApprovalReceiptInvalid {
		t.Fatalf("cryptographic receipt tamper was not detected: %#v", report)
	}

	legacy := cloneState(state)
	legacy.Approvals[0].ReceiptDecision = ""
	legacy.Approvals[0].ReceiptSignature = ""
	report = verifyStoredApprovalReceipts(legacy.Approvals, issued.registry)
	if report.Complete || report.Unavailable != 1 || report.Records[0].Verification != ApprovalReceiptUnavailable {
		t.Fatalf("legacy receipt absence was not explicit: %#v", report)
	}
}

func TestReadExistingEvidenceDoesNotCreateMissingLock(t *testing.T) {
	fixture := newStoreFixture(t, nil)
	storage, err := fixture.store.PrivateProjectStorage()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(fixture.store.lockPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("test requires absent state lock: %v", err)
	}
	if _, _, _, err := ReadExistingEvidence(context.Background(), storage, LoadedApprovalRegistry{}); err == nil {
		t.Fatal("evidence reader accepted missing state lock")
	}
	if _, err := os.Lstat(fixture.store.lockPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("evidence reader created missing state lock: %v", err)
	}
}

func TestReadExistingEvidenceDoesNotRepairUnresolvedTransaction(t *testing.T) {
	fixture := newStoreFixture(t, nil)
	storage, err := fixture.store.PrivateProjectStorage()
	if err != nil {
		t.Fatal(err)
	}
	body := []byte(`{"incomplete":true}`)
	if err := os.WriteFile(fixture.store.transactionPath, body, 0o600); err != nil {
		t.Fatal(err)
	}
	before, err := os.Stat(fixture.store.transactionPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := ReadExistingEvidence(context.Background(), storage, LoadedApprovalRegistry{}); err == nil {
		t.Fatal("evidence reader accepted unresolved state transaction")
	}
	afterBody, err := os.ReadFile(fixture.store.transactionPath)
	if err != nil {
		t.Fatal(err)
	}
	after, err := os.Stat(fixture.store.transactionPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(afterBody, body) || after.Mode() != before.Mode() || !after.ModTime().Equal(before.ModTime()) {
		t.Fatal("evidence reader modified unresolved state transaction")
	}
}
