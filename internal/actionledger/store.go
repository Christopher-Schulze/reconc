package actionledger

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"time"

	"reconc.dev/reconc/internal/action"
	"reconc.dev/reconc/internal/actionstate"
	"reconc.dev/reconc/internal/boundedio"
	"reconc.dev/reconc/internal/jsonl"
)

const (
	liveFileName        = "ledger.jsonl"
	lockFileName        = "ledger.lock"
	transactionFileName = "ledger-transaction.json"
	transactionBackup   = "ledger-transaction-backup"
	appendLockTimeout   = 2 * time.Second
	directoryReadTries  = 20
	directoryRetryDelay = 5 * time.Millisecond
)

var errRecordAlreadyCommitted = errors.New("action ledger record is already committed")

type Store struct {
	storage     actionstate.PrivateProjectStorage
	directory   string
	livePath    string
	headPath    string
	layout      jsonl.Layout
	policy      jsonl.Policy
	publishHead func(string, []byte) error
}

func OpenStore(storage actionstate.PrivateProjectStorage) (*Store, error) {
	if err := storage.Validate(); err != nil {
		return nil, fmt.Errorf("open action ledger storage: %w", err)
	}
	directory := storage.ActionDirectory()
	live := filepath.Join(directory, liveFileName)
	store := &Store{
		storage: storage, directory: directory, livePath: live,
		headPath: filepath.Join(directory, headFileName),
		layout: jsonl.Layout{
			LockPath:      filepath.Join(directory, lockFileName),
			JournalPath:   filepath.Join(directory, transactionFileName),
			BackupPrefix:  filepath.Join(directory, transactionBackup),
			DirectoryMode: 0o700, FileMode: 0o600, JournalMode: 0o600,
			LockTimeout: appendLockTimeout, Security: storage,
		},
		policy:      jsonl.Policy{MaxBytes: MaxLiveBytes, MaxArchives: MaxArchives},
		publishHead: storage.PublishPrivateFile,
	}
	return store, nil
}

// ExistingState reports whether the private project contains material ledger
// state. It never creates a lock or repairs missing state.
func (s *Store) ExistingState(ctx context.Context) (bool, error) {
	if s == nil || ctx == nil {
		return false, ledgerError(action.ReasonLedgerUnavailable, "action ledger store is required", nil)
	}
	if err := s.storage.Validate(); err != nil {
		return false, ledgerError(action.ReasonLedgerUnavailable, "validate action ledger storage", err)
	}
	lock, err := s.validateExistingLock(ctx)
	if err != nil {
		return false, err
	}
	if lock {
		return s.existingStateLocked(ctx)
	}
	material, observedLock, err := s.inspectExistingState()
	if err != nil {
		if retryLock, lockErr := s.validateExistingLock(ctx); lockErr != nil {
			return false, lockErr
		} else if retryLock {
			return s.existingStateLocked(ctx)
		}
		return false, err
	}
	if observedLock {
		return s.existingStateLocked(ctx)
	}
	if material {
		return false, ledgerError(action.ReasonLedgerCorrupt, "action ledger material exists without its lock file", nil)
	}
	return false, nil
}

func (s *Store) existingStateLocked(ctx context.Context) (bool, error) {
	material := false
	err := jsonl.WithExistingLayoutLockContext(ctx, s.livePath, s.layout, func() error {
		var inspectErr error
		material, _, inspectErr = s.inspectExistingState()
		return inspectErr
	})
	if err != nil {
		var typed *Error
		if errors.As(err, &typed) {
			return false, err
		}
		return false, ledgerError(action.ReasonLedgerUnavailable, "inspect locked action ledger state", err)
	}
	return material, nil
}

func (s *Store) inspectExistingState() (material bool, lock bool, resultErr error) {
	entries, err := s.readActionDirectorySnapshot()
	if err != nil {
		return false, false, ledgerError(action.ReasonLedgerUnavailable, "inspect action ledger directory", err)
	}
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasPrefix(name, "ledger") {
			continue
		}
		if !allowedLedgerEntry(name, true) {
			return false, false, ledgerError(action.ReasonLedgerCorrupt, "unexpected action ledger entry "+name, nil)
		}
		if name == lockFileName {
			lock = true
			continue
		}
		material = true
	}
	return material, lock, nil
}

func (s *Store) readActionDirectorySnapshot() ([]os.DirEntry, error) {
	var err error
	for attempt := 0; attempt < directoryReadTries; attempt++ {
		var entries []os.DirEntry
		entries, err = boundedio.ReadDirNoSymlink(s.directory, 4096)
		if err == nil || !errors.Is(err, boundedio.ErrDirectorySnapshotChanged) {
			return entries, err
		}
		if attempt+1 < directoryReadTries {
			time.Sleep(directoryRetryDelay)
		}
	}
	return nil, err
}

func (s *Store) validateExistingLock(ctx context.Context) (bool, error) {
	info, err := os.Lstat(s.layout.LockPath)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return false, ledgerError(action.ReasonLedgerCorrupt, "action ledger lock must be a non-symlink regular file", err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != s.layout.FileMode.Perm() {
		return false, ledgerError(action.ReasonLedgerCorrupt, fmt.Sprintf(
			"action ledger lock has mode %o; want %o", info.Mode().Perm(), s.layout.FileMode.Perm(),
		), nil)
	}
	securityErr := s.storage.ValidateJSONLFile(s.layout.LockPath, 4<<10)
	if securityErr == nil {
		return true, nil
	}
	waitErr := jsonl.WithExistingLayoutLockContext(ctx, s.livePath, s.layout, func() error {
		return nil
	})
	if waitErr == nil {
		return true, nil
	}
	if retryErr := s.storage.ValidateJSONLFile(s.layout.LockPath, 4<<10); retryErr == nil {
		return true, nil
	} else {
		return false, ledgerError(
			action.ReasonLedgerCorrupt, "validate private action ledger lock",
			errors.Join(securityErr, waitErr, retryErr),
		)
	}
}

func (s *Store) Append(ctx context.Context, record Record) (Record, error) {
	if s == nil || ctx == nil {
		return Record{}, ledgerError(action.ReasonLedgerUnavailable, "action ledger store and context are required", nil)
	}
	if err := s.storage.Validate(); err != nil {
		return Record{}, ledgerError(action.ReasonLedgerUnavailable, "validate action ledger storage", err)
	}
	if _, err := s.validateExistingLock(ctx); err != nil {
		return Record{}, err
	}
	if record.Call.RepositoryIdentity != s.storage.RepositoryIdentity() {
		return Record{}, ledgerError(action.ReasonLedgerCorrupt, "action ledger record repository identity does not match its storage", nil)
	}
	if err := s.validateRecordKeyGeneration(record); err != nil {
		return Record{}, ledgerError(action.ReasonLedgerCorrupt, "validate action ledger identity generation", err)
	}
	var sealed Record
	prepare := func() ([]byte, error) {
		if err := s.validateStablePathsAfterRecovery(); err != nil {
			return nil, wrapLedgerError(action.ReasonLedgerUnavailable, "validate action ledger paths before append", err)
		}
		records, head, _, err := s.loadVerifiedLocked()
		if err != nil {
			return nil, wrapLedgerError(action.ReasonLedgerCorrupt, "verify action ledger before append", err)
		}
		sequence := uint64(1)
		previous := ""
		if len(records) > 0 {
			last := records[len(records)-1]
			sequence = last.Sequence + 1
			previous = last.Digest
			if head == nil || head.LastSequence != last.Sequence || head.LastDigest != last.Digest {
				return nil, ledgerError(action.ReasonLedgerCorrupt, "action ledger detached head does not match the retained tail", nil)
			}
			if sameRecordInput(last, record) {
				sealed = last
				return nil, errRecordAlreadyCommitted
			}
		}
		var body []byte
		sealed, body, err = Seal(record, sequence, previous)
		if err != nil {
			return nil, ledgerError(action.ReasonLedgerCorrupt, "seal action ledger record", err)
		}
		candidate := append(append(make([]Record, 0, len(records)+1), records...), sealed)
		statuses, err := BuildCallStatuses(candidate)
		if err != nil {
			return nil, ledgerError(action.ReasonLedgerCorrupt, "validate action ledger lifecycle before append", err)
		}
		if err := s.protectActiveCallsFromRotation(body, statuses); err != nil {
			return nil, err
		}
		return body, nil
	}
	if err := jsonl.AppendTransactionContextWithLayout(
		ctx, s.livePath, s.policy, s.layout, prepare, s.commitHeadLocked,
	); err != nil {
		if errors.Is(err, errRecordAlreadyCommitted) {
			return sealed, nil
		}
		var typed *Error
		if errors.As(err, &typed) {
			return Record{}, err
		}
		return Record{}, ledgerError(action.ReasonLedgerUnavailable, "append action ledger", err)
	}
	return sealed, nil
}

func (s *Store) protectActiveCallsFromRotation(record []byte, statuses []CallStatus) error {
	info, err := os.Lstat(s.livePath)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return ledgerError(action.ReasonLedgerUnavailable, "inspect live action ledger before rotation", err)
	}
	if info.Size()+int64(len(record))+1 <= s.policy.MaxBytes {
		return nil
	}
	droppedThrough, err := s.oldestArchiveLastSequence()
	if err != nil || droppedThrough == 0 {
		return err
	}
	for _, status := range statuses {
		if !status.TerminalComplete && status.FirstSequence <= droppedThrough {
			return ledgerError(
				action.ReasonLedgerUnavailable,
				fmt.Sprintf("action ledger rotation would prune active call %s through sequence %d", status.CallID, droppedThrough),
				nil,
			)
		}
	}
	return nil
}

func (s *Store) oldestArchiveLastSequence() (uint64, error) {
	name := fmt.Sprintf("%s.%d", liveFileName, MaxArchives)
	body, err := s.storage.ReadPrivateFile(name, MaxLiveBytes)
	if errors.Is(err, os.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, ledgerError(action.ReasonLedgerUnavailable, "read oldest action ledger archive before rotation", err)
	}
	if len(body) == 0 || body[len(body)-1] != '\n' {
		return 0, ledgerError(action.ReasonLedgerCorrupt, "oldest action ledger archive has a partial tail", nil)
	}
	trimmed := body[:len(body)-1]
	if separator := bytes.LastIndexByte(trimmed, '\n'); separator >= 0 {
		trimmed = trimmed[separator+1:]
	}
	record, err := Decode(trimmed)
	if err != nil {
		return 0, ledgerError(action.ReasonLedgerCorrupt, "decode oldest action ledger archive tail", err)
	}
	return record.Sequence, nil
}

func sameRecordInput(sealed, candidate Record) bool {
	clearEnvelope := func(record *Record) {
		record.Schema = ""
		record.FormatVersion = ""
		record.ChainVersion = ""
		record.Sequence = 0
		record.PreviousDigest = ""
		record.Digest = ""
	}
	clearEnvelope(&sealed)
	clearEnvelope(&candidate)
	return reflect.DeepEqual(sealed, candidate)
}

func (s *Store) validateRecordKeyGeneration(record Record) error {
	return s.storage.WithIdentity(func(key *actionstate.IdentityKey) error {
		identities := []string{
			record.Call.RequestIdentity, record.Call.RepositoryIdentity, record.Call.ServerFingerprint,
			record.Call.RunIdentity, record.Call.SessionIdentity, record.Call.ContextIdentity,
		}
		if record.Call.Tool.Mode == action.LedgerKeyedName {
			identities = append(identities, record.Call.Tool.Value)
		}
		for _, field := range record.SelectedFields {
			identities = append(identities, field.PointerIdentity, field.ValueIdentity)
		}
		if record.Budget != nil {
			identities = append(identities, record.Budget.StateVersion)
			if record.Budget.ReservationIdentity != "absent" {
				identities = append(identities, record.Budget.ReservationIdentity)
			}
		}
		if record.Dispatch != nil {
			if record.Dispatch.ReservationIdentity != "absent" {
				identities = append(identities, record.Dispatch.ReservationIdentity)
			}
		}
		for _, identity := range identities {
			if identity == "" {
				continue
			}
			keyID, valid := action.KeyedIdentityKeyID(identity)
			if !valid || keyID != key.ID() {
				return fmt.Errorf("action ledger record mixes identity-key generations")
			}
		}
		return nil
	})
}

func (s *Store) Recover(ctx context.Context) error {
	if s == nil || ctx == nil {
		return ledgerError(action.ReasonLedgerUnavailable, "action ledger store and context are required", nil)
	}
	if err := s.storage.Validate(); err != nil {
		return ledgerError(action.ReasonLedgerUnavailable, "validate action ledger storage", err)
	}
	if _, err := s.validateExistingLock(ctx); err != nil {
		return err
	}
	err := jsonl.ReadSnapshotContextWithLayout(
		ctx, s.livePath, s.layout, s.commitHeadLocked, s.validateStablePathsAfterRecovery,
	)
	if err == nil {
		return nil
	}
	var typed *Error
	if errors.As(err, &typed) {
		return err
	}
	return ledgerError(action.ReasonLedgerUnavailable, "recover action ledger", err)
}

func (s *Store) commitHeadLocked() error {
	records, err := s.loadRecordsLocked()
	if err != nil {
		return wrapLedgerError(action.ReasonLedgerCorrupt, "read retained action ledger for detached-head commit", err)
	}
	if len(records) == 0 {
		return ledgerError(action.ReasonLedgerCorrupt, "cannot commit a detached head without a retained record", nil)
	}
	last := records[len(records)-1]
	head, err := s.loadHead()
	if err != nil {
		return wrapLedgerError(action.ReasonLedgerCorrupt, "read detached head for commit", err)
	}
	if head != nil && head.LastSequence == last.Sequence && head.LastDigest == last.Digest {
		return nil
	}
	var next chainHead
	if head == nil {
		if last.Sequence != 1 || len(records) != 1 {
			return ledgerError(action.ReasonLedgerCorrupt, "action ledger detached head is missing for an existing chain", nil)
		}
		next = newChainHead(last)
	} else {
		next, err = advanceChainHead(*head, last)
		if err != nil {
			return ledgerError(action.ReasonLedgerCorrupt, "advance action ledger detached head", err)
		}
	}
	body, err := encodeChainHead(next)
	if err != nil {
		return ledgerError(action.ReasonLedgerCorrupt, "encode action ledger detached head", err)
	}
	if s.publishHead == nil {
		return ledgerError(action.ReasonLedgerUnavailable, "action ledger detached-head publisher is unavailable", nil)
	}
	if err := s.publishHead(headFileName, body); err != nil {
		return ledgerError(action.ReasonLedgerUnavailable, "publish action ledger detached head", err)
	}
	return nil
}

func (s *Store) loadHead() (*chainHead, error) {
	body, err := s.storage.ReadPrivateFile(headFileName, maxHeadBytes)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, ledgerError(action.ReasonLedgerUnavailable, "read action ledger detached head", err)
	}
	head, err := decodeChainHead(body)
	if err != nil {
		return nil, ledgerError(action.ReasonLedgerCorrupt, "decode action ledger detached head", err)
	}
	if head.RepositoryIdentity != s.storage.RepositoryIdentity() {
		return nil, ledgerError(action.ReasonLedgerCorrupt, "action ledger detached head repository identity drifted", nil)
	}
	return &head, nil
}

func allowedLedgerEntry(name string, transactionFiles bool) bool {
	switch name {
	case liveFileName, liveFileName + ".1", liveFileName + ".2", headFileName, lockFileName:
		return true
	case transactionFileName:
		return transactionFiles
	}
	if transactionFiles && strings.HasPrefix(name, transactionBackup+".") {
		suffix := strings.TrimPrefix(name, transactionBackup+".")
		return suffix == "0" || suffix == "1" || suffix == "2"
	}
	return false
}
