package actionstate

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"reconc.dev/reconc/internal/action"
	"reconc.dev/reconc/internal/atomicfile"
	"reconc.dev/reconc/internal/retention"
)

type StoreOptions struct {
	Home        string
	Repository  string
	KeyLease    *IdentityKeyLease
	Clock       TrustedClock
	OwnerID     string
	LockTimeout time.Duration
}

type Store struct {
	home               string
	repository         string
	repositoryIdentity string
	projectKey         string
	directory          string
	statePath          string
	transactionPath    string
	lockPath           string
	key                *IdentityKey
	keyLease           *IdentityKeyLease
	clock              TrustedClock
	ownerID            string
	lockTimeout        time.Duration
	publish            func(string, []byte) error
}

func OpenStore(options StoreOptions) (*Store, error) {
	key, releaseKey, err := options.KeyLease.acquireUse()
	if err != nil {
		return nil, fmt.Errorf("an active identity-key lease is required: %w", err)
	}
	defer releaseKey()
	home, err := ResolveHome(options.Home)
	if err != nil {
		return nil, err
	}
	repository, identity, err := ObserveRepository(key, options.Repository)
	if err != nil {
		return nil, err
	}
	if pathContained(repository, home) {
		return nil, fmt.Errorf("reconc action state must be outside the canonical repository")
	}
	projectDir := retention.ProjectDir(home, repository)
	projectKey := filepath.Base(projectDir)
	directory, err := ensureActionStateDirectory(home, projectKey)
	if err != nil {
		return nil, err
	}
	clock := options.Clock
	if clock == nil {
		clock = SystemClock{}
	}
	ownerID := options.OwnerID
	if ownerID == "" {
		ownerID, err = randomID("own_", secureRandomReader)
		if err != nil {
			return nil, err
		}
	}
	if !validOpaqueStateIdentity(ownerID) {
		return nil, fmt.Errorf("action state owner identity is invalid")
	}
	timeout := options.LockTimeout
	if timeout == 0 {
		timeout = StateLockTimeout
	}
	if timeout < time.Millisecond || timeout > StateLockTimeout {
		return nil, fmt.Errorf("state lock timeout must be between 1ms and %s", StateLockTimeout)
	}
	return &Store{
		home: home, repository: repository, repositoryIdentity: identity,
		projectKey: projectKey, directory: directory,
		statePath:       filepath.Join(directory, "state.json"),
		transactionPath: filepath.Join(directory, "state-transaction.json"),
		lockPath:        filepath.Join(directory, "state.lock"),
		key:             key, keyLease: options.KeyLease,
		clock: clock, ownerID: ownerID, lockTimeout: timeout,
		publish: publishPrivateFile,
	}, nil
}

func ensureActionStateDirectory(home, projectKey string) (directory string, resultErr error) {
	lockPath := filepath.Join(home, retention.ProjectRootRetentionLockName)
	// Directory creation and Windows DACL publication are one private-boundary
	// transition. Serialize creators as well as retention so another process
	// cannot observe the directory between those two operations.
	lock, err := acquireFileLock(context.Background(), lockPath, StateLockTimeout)
	if err != nil {
		return "", fmt.Errorf("coordinate action state with project retention: %w", err)
	}
	defer func() { resultErr = errors.Join(resultErr, lock.close()) }()
	directory, err = ensurePrivateSubdirectories(home, "projects", projectKey, "action")
	if err != nil {
		return "", err
	}
	return directory, nil
}

var secureRandomReader io.Reader = cryptoRandomReader{}

type cryptoRandomReader struct{}

func (cryptoRandomReader) Read(buffer []byte) (int, error) {
	return readCryptoRandom(buffer)
}

func pathContained(root, candidate string) bool {
	relative, err := filepath.Rel(root, candidate)
	if err != nil {
		return false
	}
	if runtime.GOOS == "windows" {
		relative = strings.ToLower(relative)
	}
	return relative == "." || relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

func (s *Store) withLock(ctx context.Context, operation func() error) (resultErr error) {
	if s == nil || operation == nil {
		return stateError(action.ReasonStateUnavailable, "action state store is unavailable", nil)
	}
	key, releaseKey, err := s.keyLease.acquireUse()
	if err != nil || key != s.key {
		if releaseKey != nil {
			releaseKey()
		}
		return stateError(action.ReasonIdentityUnavailable, "action identity-key lease is inactive", err)
	}
	defer releaseKey()
	if err := s.validatePrivateDirectories(); err != nil {
		return stateError(action.ReasonStateUnavailable, "validate private action-state directories", err)
	}
	lock, err := acquireFileLock(ctx, s.lockPath, s.lockTimeout)
	if err != nil {
		return stateError(action.ReasonStateUnavailable, "acquire action state transaction lock", err)
	}
	defer func() { resultErr = errors.Join(resultErr, lock.close()) }()
	if err := s.recoverTransaction(); err != nil {
		return err
	}
	return operation()
}

func (s *Store) validatePrivateDirectories() error {
	paths := []string{
		s.home,
		filepath.Join(s.home, "action"),
		filepath.Join(s.home, "projects"),
		filepath.Join(s.home, "projects", s.projectKey),
		s.directory,
	}
	for index, path := range paths {
		if index > 0 && path == paths[index-1] {
			continue
		}
		if err := validatePrivateDirectory(path); err != nil {
			return fmt.Errorf("validate %s: %w", path, err)
		}
	}
	return nil
}

func (s *Store) loadState() (State, bool, error) {
	body, err := readPrivateRegularFile(s.statePath, MaxStateBytes)
	if errors.Is(err, os.ErrNotExist) {
		state, stateErr := s.initialState()
		return state, false, stateErr
	}
	if err != nil {
		return State{}, false, stateError(action.ReasonStateCorrupt, "read bounded regular action state", err)
	}
	state, err := s.decodeState(body)
	if err != nil {
		return State{}, false, err
	}
	return state, true, nil
}

func (s *Store) initialState() (State, error) {
	state := State{
		Schema: StateSchema, FormatVersion: StateFormatVersion,
		KeyID: s.key.ID(), RepositoryIdentity: s.repositoryIdentity,
		Budgets: []BudgetRecord{}, Reservations: []Reservation{}, TerminalCalls: []TerminalCall{},
		Approvals: []ApprovalRecord{},
	}
	digest, err := s.stateDigest(state)
	if err != nil {
		return State{}, err
	}
	state.Digest = digest
	return state, nil
}

func (s *Store) decodeState(body []byte) (State, error) {
	var state State
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&state); err != nil {
		return State{}, stateError(action.ReasonStateCorrupt, "decode action state", err)
	}
	if err := requireJSONEOF(decoder); err != nil {
		return State{}, stateError(action.ReasonStateCorrupt, "decode action state", err)
	}
	if err := s.validateState(state, true); err != nil {
		return State{}, err
	}
	return state, nil
}

func (s *Store) writeState(previous State, previousPersisted bool, next *State) error {
	if next == nil {
		return stateError(action.ReasonStateUnavailable, "next action state is unavailable", nil)
	}
	if s.publish == nil {
		return stateError(action.ReasonStateUnavailable, "action state publisher is unavailable", nil)
	}
	if previous.Revision == ^uint64(0) {
		return stateError(action.ReasonStateCorrupt, "action state revision saturated", nil)
	}
	if err := s.validateState(previous, previousPersisted); err != nil {
		return err
	}
	next.Revision = previous.Revision + 1
	digest, err := s.stateDigest(*next)
	if err != nil {
		return err
	}
	next.Digest = digest
	if err := s.validateState(*next, true); err != nil {
		return err
	}
	transaction, err := s.newTransaction(previous, previousPersisted, *next)
	if err != nil {
		return err
	}
	stateBody, err := encodeBoundedJSON(*next, MaxStateBytes)
	if err != nil {
		return stateError(action.ReasonStateUnavailable, "encode bounded action state", err)
	}
	transactionBody, err := encodeBoundedCompactJSON(transaction, MaxStateTransaction)
	if err != nil {
		return stateError(action.ReasonStateUnavailable, "encode bounded action state transaction", err)
	}
	if err := s.publish(s.transactionPath, transactionBody); err != nil {
		return stateError(action.ReasonStateUnavailable, "publish action state transaction", err)
	}
	if err := s.publish(s.statePath, stateBody); err != nil {
		return stateError(action.ReasonStateUnavailable, "publish action state", err)
	}
	if err := removeAndSync(s.transactionPath, s.directory); err != nil {
		return stateError(action.ReasonStateUnavailable, "finalize action state transaction", err)
	}
	return nil
}

func encodeBoundedJSON(value any, maximum int) ([]byte, error) {
	body, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return nil, err
	}
	body = append(body, '\n')
	if len(body) > maximum {
		return nil, fmt.Errorf("encoded state exceeds %d bytes", maximum)
	}
	return body, nil
}

func encodeBoundedCompactJSON(value any, maximum int) ([]byte, error) {
	body, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	body = append(body, '\n')
	if len(body) > maximum {
		return nil, fmt.Errorf("encoded state exceeds %d bytes", maximum)
	}
	return body, nil
}

func publishPrivateFile(path string, body []byte) error {
	_, err := publishPrivateFileIfChanged(path, body)
	return err
}

func publishPrivateFileIfChanged(path string, body []byte) (bool, error) {
	directory := filepath.Dir(path)
	if err := validatePrivateDirectory(directory); err != nil {
		return false, fmt.Errorf("validate private publication directory: %w", err)
	}
	changed, err := atomicfile.WriteIfChanged(path, body, 0o600)
	if err != nil {
		return false, err
	}
	if changed {
		if err := securePublishedPrivateFile(path); err != nil {
			return false, err
		}
	}
	if err := validatePrivateDirectory(directory); err != nil {
		return false, err
	}
	return changed, nil
}

func writeBoundedJSON(path string, value any, maximum int) error {
	body, err := encodeBoundedJSON(value, maximum)
	if err != nil {
		return err
	}
	return publishPrivateFile(path, body)
}

func (s *Store) writeBoundedJSON(path string, value any, maximum int) error {
	if s == nil || s.publish == nil {
		return fmt.Errorf("action state publisher is unavailable")
	}
	body, err := encodeBoundedJSON(value, maximum)
	if err != nil {
		return err
	}
	return s.publish(path, body)
}

func (s *Store) newTransaction(previous State, persisted bool, next State) (stateTransaction, error) {
	transaction := stateTransaction{
		Schema: TransactionSchema, FormatVersion: StateFormatVersion,
		BeforePersisted: persisted, BeforeRevision: previous.Revision,
		BeforeDigest: previous.Digest, After: next,
	}
	digest, err := s.transactionDigest(transaction)
	if err != nil {
		return stateTransaction{}, err
	}
	transaction.Digest = digest
	return transaction, nil
}

func (s *Store) recoverTransaction() error {
	body, err := readPrivateRegularFile(s.transactionPath, MaxStateTransaction)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return stateError(action.ReasonStateCorrupt, "read action state transaction", err)
	}
	transaction, err := s.decodeTransaction(body)
	if err != nil {
		return err
	}
	current, persisted, err := s.loadStateWithoutRecovery()
	if err != nil {
		return err
	}
	if persisted && current.Digest == transaction.After.Digest {
		return removeAndSync(s.transactionPath, s.directory)
	}
	if persisted != transaction.BeforePersisted || current.Digest != transaction.BeforeDigest {
		return stateError(action.ReasonStateCorrupt, "action state and recovery transaction diverged", nil)
	}
	if err := s.writeBoundedJSON(s.statePath, transaction.After, MaxStateBytes); err != nil {
		return stateError(action.ReasonStateUnavailable, "recover action state transaction", err)
	}
	return removeAndSync(s.transactionPath, s.directory)
}

func (s *Store) loadStateWithoutRecovery() (State, bool, error) {
	return s.loadState()
}

func (s *Store) decodeTransaction(body []byte) (stateTransaction, error) {
	var transaction stateTransaction
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&transaction); err != nil {
		return stateTransaction{}, stateError(action.ReasonStateCorrupt, "decode action state transaction", err)
	}
	if err := requireJSONEOF(decoder); err != nil {
		return stateTransaction{}, stateError(action.ReasonStateCorrupt, "decode action state transaction", err)
	}
	if transaction.Schema != TransactionSchema || transaction.FormatVersion != StateFormatVersion ||
		!identityUsesKey(transaction.BeforeDigest, s.key.ID()) ||
		transaction.BeforePersisted != (transaction.BeforeRevision > 0) ||
		transaction.BeforeRevision == ^uint64(0) ||
		transaction.After.Revision != transaction.BeforeRevision+1 ||
		transaction.After.Digest == transaction.BeforeDigest {
		return stateTransaction{}, stateError(action.ReasonStateCorrupt, "action state transaction metadata is invalid", nil)
	}
	if err := s.validateState(transaction.After, true); err != nil {
		return stateTransaction{}, err
	}
	digest, err := s.transactionDigest(transaction)
	if err != nil || !constantIdentityEqual(digest, transaction.Digest) {
		return stateTransaction{}, stateError(action.ReasonStateCorrupt, "action state transaction digest is invalid", err)
	}
	return transaction, nil
}

func removeAndSync(path, directory string) error {
	if filepath.Clean(filepath.Dir(path)) != filepath.Clean(directory) {
		return fmt.Errorf("transaction cleanup path escapes its private directory")
	}
	if err := validatePrivateDirectory(directory); err != nil {
		return err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return fmt.Errorf("transaction cleanup target must be a non-symlink regular file")
	}
	if err := os.Remove(path); err != nil {
		return err
	}
	return syncStateDirectory(directory)
}

func (s *Store) resampleRepositoryIdentity() error {
	resolved, identity, err := ObserveRepository(s.key, s.repository)
	if err != nil {
		return stateError(action.ReasonIdentityUnavailable, "resample repository identity", err)
	}
	if resolved != s.repository || identity != s.repositoryIdentity {
		return stateError(action.ReasonIdentityUnavailable, "repository identity changed after store creation", nil)
	}
	return nil
}

func (s *Store) trustedNow(state State) (ClockSnapshot, error) {
	snapshot, err := s.clock.Snapshot()
	if err != nil {
		return ClockSnapshot{}, stateError(action.ReasonStateUnavailable, "read trusted action-state clock", err)
	}
	snapshot.Time = snapshot.Time.UTC()
	if snapshot.Time.IsZero() || snapshot.Time.UnixNano() <= 0 || !action.SafeLabel(snapshot.Source) {
		return ClockSnapshot{}, stateError(action.ReasonStateUnavailable, "trusted clock snapshot is invalid", nil)
	}
	if state.ClockSource != "" && state.ClockSource != snapshot.Source {
		return ClockSnapshot{}, stateError(action.ReasonStateUnavailable, "trusted clock source changed", nil)
	}
	if state.LastObservedUnixNano != 0 && snapshot.Time.UnixNano() < state.LastObservedUnixNano {
		return ClockSnapshot{}, stateError(action.ReasonStateUnavailable, "trusted clock moved backward", nil)
	}
	return snapshot, nil
}

func (s *Store) applyClock(state *State, snapshot ClockSnapshot) {
	state.ClockSource = snapshot.Source
	state.LastObservedUnixNano = snapshot.Time.UnixNano()
}
