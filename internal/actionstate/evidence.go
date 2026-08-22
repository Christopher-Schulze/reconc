package actionstate

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"reconc.dev/reconc/internal/action"
	"reconc.dev/reconc/internal/actionapproval"
)

type ApprovalReceiptVerificationStatus string

const (
	ApprovalReceiptVerified      ApprovalReceiptVerificationStatus = "verified"
	ApprovalReceiptUnavailable   ApprovalReceiptVerificationStatus = "unavailable"
	ApprovalReceiptInvalid       ApprovalReceiptVerificationStatus = "invalid"
	ApprovalReceiptNotApplicable ApprovalReceiptVerificationStatus = "not_applicable"
)

type ApprovalReceiptVerification struct {
	RequestID        string                            `json:"request_id"`
	CallID           string                            `json:"call_id"`
	ApprovalStatus   actionapproval.Status             `json:"approval_status"`
	Verification     ApprovalReceiptVerificationStatus `json:"verification"`
	RegistryIdentity string                            `json:"registry_identity,omitempty"`
	AuthorityKeyID   string                            `json:"authority_key_id,omitempty"`
	ReceiptID        string                            `json:"receipt_id,omitempty"`
	ReceiptIdentity  string                            `json:"receipt_identity,omitempty"`
}

type ApprovalReceiptVerificationReport struct {
	Evaluated   bool                          `json:"evaluated"`
	Complete    bool                          `json:"complete"`
	Applicable  int                           `json:"applicable"`
	Verified    int                           `json:"verified"`
	Unavailable int                           `json:"unavailable"`
	Invalid     int                           `json:"invalid"`
	Records     []ApprovalReceiptVerification `json:"records"`
}

// ReadExistingEvidence binds a narrowly scoped reader to already validated
// private project storage. It does not expose mutation methods, create a
// project directory, repair state, or create an identity key.
func ReadExistingEvidence(
	ctx context.Context,
	storage PrivateProjectStorage,
	registry LoadedApprovalRegistry,
) (StateStatus, ApprovalReceiptVerificationReport, bool, error) {
	store, err := openExistingStore(storage)
	if err != nil {
		return StateStatus{}, ApprovalReceiptVerificationReport{}, false, err
	}
	return store.evidenceSnapshot(ctx, registry)
}

func openExistingStore(storage PrivateProjectStorage) (*Store, error) {
	if err := storage.Validate(); err != nil {
		return nil, fmt.Errorf("open existing action state storage: %w", err)
	}
	return &Store{
		home: storage.home, repository: storage.repository,
		repositoryIdentity: storage.repositoryIdentity, projectKey: storage.projectKey,
		directory: storage.directory, statePath: filepath.Join(storage.directory, "state.json"),
		transactionPath: filepath.Join(storage.directory, "state-transaction.json"),
		lockPath:        filepath.Join(storage.directory, "state.lock"), key: storage.key,
		keyLease: storage.keyLease, clock: SystemClock{}, ownerID: "evidence-reader-v1",
		lockTimeout: StateLockTimeout, publish: publishPrivateFile,
	}, nil
}

// evidenceSnapshot returns one integrity-checked state and receipt snapshot.
// Receipt signatures remain private state material and are never returned.
func (s *Store) evidenceSnapshot(
	ctx context.Context,
	registry LoadedApprovalRegistry,
) (status StateStatus, receipts ApprovalReceiptVerificationReport, persisted bool, resultErr error) {
	if s == nil || ctx == nil {
		return StateStatus{}, ApprovalReceiptVerificationReport{}, false,
			stateError(action.ReasonStateUnavailable, "action state evidence reader is unavailable", nil)
	}
	key, releaseKey, err := s.keyLease.acquireUse()
	if err != nil || key != s.key {
		if releaseKey != nil {
			releaseKey()
		}
		return StateStatus{}, ApprovalReceiptVerificationReport{}, false,
			stateError(action.ReasonIdentityUnavailable, "action identity-key lease is inactive", err)
	}
	defer releaseKey()
	if err := s.validatePrivateDirectories(); err != nil {
		return StateStatus{}, ApprovalReceiptVerificationReport{}, false, err
	}
	lock, err := acquireExistingSharedFileLock(ctx, s.lockPath, s.lockTimeout)
	if err != nil {
		return StateStatus{}, ApprovalReceiptVerificationReport{}, false,
			stateError(action.ReasonStateUnavailable, "acquire existing action state evidence lock", err)
	}
	defer func() { resultErr = errors.Join(resultErr, lock.close()) }()
	if _, err := os.Lstat(s.transactionPath); err == nil {
		return StateStatus{}, ApprovalReceiptVerificationReport{}, false,
			stateError(action.ReasonStateUnavailable, "action state has an unresolved transaction", nil)
	} else if !errors.Is(err, os.ErrNotExist) {
		return StateStatus{}, ApprovalReceiptVerificationReport{}, false,
			stateError(action.ReasonStateUnavailable, "inspect action state transaction", err)
	}
	if err := s.resampleRepositoryIdentity(); err != nil {
		return StateStatus{}, ApprovalReceiptVerificationReport{}, false, err
	}
	state, exists, persistedBytes, err := s.loadStateWithSize()
	if err != nil {
		return StateStatus{}, ApprovalReceiptVerificationReport{}, false, err
	}
	if !exists {
		return StateStatus{}, ApprovalReceiptVerificationReport{}, false,
			stateError(action.ReasonStateUnavailable, "persisted action state is absent", nil)
	}
	if _, err := s.trustedNow(state); err != nil {
		return StateStatus{}, ApprovalReceiptVerificationReport{}, false, err
	}
	status, err = statusFromState(state, persistedBytes)
	if err != nil {
		return StateStatus{}, ApprovalReceiptVerificationReport{}, false, err
	}
	receipts = verifyStoredApprovalReceipts(state.Approvals, registry)
	return status, receipts, true, resultErr
}

func verifyStoredApprovalReceipts(
	records []ApprovalRecord,
	registry LoadedApprovalRegistry,
) ApprovalReceiptVerificationReport {
	report := ApprovalReceiptVerificationReport{
		Evaluated: true, Complete: true, Records: make([]ApprovalReceiptVerification, len(records)),
	}
	compiled := registry.compiled()
	for index, record := range records {
		view := ApprovalReceiptVerification{
			RequestID: record.Request.RequestID, CallID: record.Request.CallID,
			ApprovalStatus: record.Status, Verification: ApprovalReceiptNotApplicable,
			RegistryIdentity: record.RegistryIdentity, AuthorityKeyID: record.AuthorityKeyID,
			ReceiptID: record.ReceiptID, ReceiptIdentity: record.ReceiptIdentity,
		}
		if record.Status != actionapproval.StatusApproved && record.Status != actionapproval.StatusRejected {
			report.Records[index] = view
			continue
		}
		report.Applicable++
		view.Verification = verifyStoredApprovalReceipt(record, registry, compiled)
		switch view.Verification {
		case ApprovalReceiptVerified:
			report.Verified++
		case ApprovalReceiptUnavailable:
			report.Unavailable++
			report.Complete = false
		case ApprovalReceiptInvalid:
			report.Invalid++
			report.Complete = false
		}
		report.Records[index] = view
	}
	return report
}

func verifyStoredApprovalReceipt(
	record ApprovalRecord,
	registry LoadedApprovalRegistry,
	compiled *actionapproval.CompiledRegistry,
) ApprovalReceiptVerificationStatus {
	if record.ReceiptDecision == "" || record.ReceiptSignature == "" || compiled == nil ||
		registry.Identity() != record.RegistryIdentity {
		return ApprovalReceiptUnavailable
	}
	receipt := actionapproval.Receipt{
		Schema: actionapproval.ReceiptSchema, FormatVersion: actionapproval.FormatVersion,
		Request: cloneApprovalRequest(record.Request), Decision: record.ReceiptDecision,
		AuthorityKeyID: record.AuthorityKeyID, ReceiptID: record.ReceiptID,
		SignedAt: record.ReceiptSignedAt, Signature: record.ReceiptSignature,
	}
	body, err := actionapproval.EncodeReceipt(receipt)
	if err != nil {
		return ApprovalReceiptInvalid
	}
	signedAt, err := time.Parse(time.RFC3339Nano, receipt.SignedAt)
	if err != nil {
		return ApprovalReceiptInvalid
	}
	verified, err := actionapproval.VerifySignedReceipt(compiled, record.Request, body, signedAt)
	if err != nil || !constantIdentityEqual(verified.Identity, record.ReceiptIdentity) ||
		verified.Receipt.Decision != record.ReceiptDecision {
		return ApprovalReceiptInvalid
	}
	return ApprovalReceiptVerified
}
