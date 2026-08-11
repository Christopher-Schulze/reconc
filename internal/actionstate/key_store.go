package actionstate

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"

	"reconc.dev/reconc/internal/boundedio"
)

const (
	identityKeyFormat  = "1"
	maxIdentityKeyFile = 4096
)

type identityKeyRecord struct {
	FormatVersion string `json:"format_version"`
	KeyID         string `json:"key_id"`
	CreatedAt     string `json:"created_at"`
	Key           string `json:"key"`
}

type IdentityKeyLease struct {
	Key  *IdentityKey
	lock *heldLock
	mu   sync.RWMutex
}

func CreateIdentityKey(home string, now time.Time) (string, error) {
	key, err := createIdentityKey(home, now, rand.Reader)
	if err != nil {
		return "", err
	}
	return key.ID(), nil
}

func createIdentityKey(home string, now time.Time, entropy io.Reader) (key *IdentityKey, resultErr error) {
	paths, err := prepareKeyDirectory(home)
	if err != nil {
		return nil, err
	}
	lock, err := acquireFileLock(context.Background(), filepath.Join(paths.action, "identity-key.lock"), StateLockTimeout)
	if err != nil {
		return nil, err
	}
	defer func() { resultErr = errors.Join(resultErr, lock.close()) }()
	path := filepath.Join(paths.action, "identity-key.json")
	if info, inspectErr := os.Lstat(path); inspectErr == nil {
		if !info.Mode().IsRegular() {
			return nil, fmt.Errorf("identity key path exists and is not regular")
		}
		return nil, fmt.Errorf("identity key already exists")
	} else if !errors.Is(inspectErr, os.ErrNotExist) {
		return nil, fmt.Errorf("inspect identity key: %w", inspectErr)
	}
	key, err = generateIdentityKey(entropy)
	if err != nil {
		return nil, err
	}
	if err := writeIdentityKey(path, key, now); err != nil {
		return nil, err
	}
	return key, nil
}

func AcquireIdentityKey(ctx context.Context, home string) (*IdentityKeyLease, error) {
	paths, err := prepareKeyDirectory(home)
	if err != nil {
		return nil, err
	}
	lock, err := acquireSharedFileLock(ctx, filepath.Join(paths.action, "identity-key.lock"), StateLockTimeout)
	if err != nil {
		return nil, err
	}
	key, err := readIdentityKey(filepath.Join(paths.action, "identity-key.json"))
	if err != nil {
		return nil, errors.Join(err, lock.close())
	}
	return &IdentityKeyLease{Key: key, lock: lock}, nil
}

// AcquireExistingIdentityKey opens an already initialized key owner without
// creating directories, lock files, or key material. It is the read-only
// entrypoint for ledger inspection commands.
func AcquireExistingIdentityKey(ctx context.Context, home string) (*IdentityKeyLease, error) {
	resolved, err := ResolveHome(home)
	if err != nil {
		return nil, err
	}
	actionDirectory := filepath.Join(resolved, "action")
	if err := validatePrivateDirectory(resolved); err != nil {
		return nil, fmt.Errorf("validate existing Reconc home: %w", err)
	}
	if err := validatePrivateDirectory(actionDirectory); err != nil {
		return nil, fmt.Errorf("validate existing action key directory: %w", err)
	}
	lock, err := acquireExistingSharedFileLock(
		ctx, filepath.Join(actionDirectory, "identity-key.lock"), StateLockTimeout,
	)
	if err != nil {
		return nil, err
	}
	key, err := readIdentityKey(filepath.Join(actionDirectory, "identity-key.json"))
	if err != nil {
		return nil, errors.Join(err, lock.close())
	}
	return &IdentityKeyLease{Key: key, lock: lock}, nil
}

func (l *IdentityKeyLease) Close() error {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.lock == nil {
		return nil
	}
	err := l.lock.close()
	l.lock = nil
	return err
}

func (l *IdentityKeyLease) acquireUse() (*IdentityKey, func(), error) {
	if l == nil {
		return nil, nil, fmt.Errorf("identity-key lease is unavailable")
	}
	l.mu.RLock()
	if l.lock == nil || l.Key == nil {
		l.mu.RUnlock()
		return nil, nil, fmt.Errorf("identity-key lease is closed")
	}
	return l.Key, l.mu.RUnlock, nil
}

func RotateIdentityKey(home string, now time.Time) (string, error) {
	key, err := rotateIdentityKey(home, now)
	if err != nil {
		return "", err
	}
	return key.ID(), nil
}

func rotateIdentityKey(home string, now time.Time) (key *IdentityKey, resultErr error) {
	paths, err := prepareKeyDirectory(home)
	if err != nil {
		return nil, err
	}
	lock, err := acquireFileLock(context.Background(), filepath.Join(paths.action, "identity-key.lock"), StateLockTimeout)
	if err != nil {
		return nil, err
	}
	defer func() { resultErr = errors.Join(resultErr, lock.close()) }()
	path := filepath.Join(paths.action, "identity-key.json")
	existing, err := readIdentityKey(path)
	if err != nil {
		return nil, err
	}
	if existing == nil {
		return nil, fmt.Errorf("existing identity key is unavailable")
	}
	dependent, err := dependentActionStateExists(paths.home)
	if err != nil {
		return nil, err
	}
	if dependent {
		return nil, fmt.Errorf("identity key rotation blocked: dependent action state exists; keep the current key active or perform an explicit atomic state migration/reset")
	}
	key, err = generateIdentityKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	if err := writeIdentityKey(path, key, now); err != nil {
		return nil, err
	}
	return key, nil
}

type keyPaths struct {
	home   string
	action string
}

func prepareKeyDirectory(home string) (keyPaths, error) {
	resolved, err := ResolveHome(home)
	if err != nil {
		return keyPaths{}, err
	}
	actionDir, err := ensurePrivateSubdirectories(resolved, "action")
	if err != nil {
		return keyPaths{}, err
	}
	return keyPaths{home: resolved, action: actionDir}, nil
}

func writeIdentityKey(path string, key *IdentityKey, now time.Time) error {
	if key == nil || now.IsZero() {
		return fmt.Errorf("identity key record requires key material and creation time")
	}
	record := identityKeyRecord{
		FormatVersion: identityKeyFormat, KeyID: key.ID(),
		CreatedAt: now.UTC().Format(time.RFC3339Nano),
		Key:       base64.RawURLEncoding.EncodeToString(key.material[:]),
	}
	body, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return fmt.Errorf("encode identity key: %w", err)
	}
	body = append(body, '\n')
	if len(body) > maxIdentityKeyFile {
		return fmt.Errorf("identity key record exceeds %d bytes", maxIdentityKeyFile)
	}
	changed, err := publishPrivateFileIfChanged(path, body)
	if err != nil {
		return fmt.Errorf("publish identity key: %w", err)
	}
	if !changed {
		return fmt.Errorf("identity key publication did not change the target")
	}
	return nil
}

func readIdentityKey(path string) (*IdentityKey, error) {
	body, err := readPrivateRegularFile(path, maxIdentityKeyFile)
	if err != nil {
		return nil, fmt.Errorf("read private identity key: %w", err)
	}
	var record identityKeyRecord
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&record); err != nil {
		return nil, fmt.Errorf("decode identity key: %w", err)
	}
	if err := requireJSONEOF(decoder); err != nil {
		return nil, fmt.Errorf("decode identity key: %w", err)
	}
	if record.FormatVersion != identityKeyFormat || !validLowerHex(record.KeyID, 32) {
		return nil, fmt.Errorf("identity key metadata is invalid")
	}
	created, err := time.Parse(time.RFC3339Nano, record.CreatedAt)
	if err != nil || created.IsZero() || record.CreatedAt != created.UTC().Format(time.RFC3339Nano) {
		return nil, fmt.Errorf("identity key creation time is invalid or non-canonical")
	}
	material, err := base64.RawURLEncoding.Strict().DecodeString(record.Key)
	if err != nil || base64.RawURLEncoding.EncodeToString(material) != record.Key {
		return nil, fmt.Errorf("identity key material is invalid or non-canonical")
	}
	key, err := newIdentityKey(material)
	for index := range material {
		material[index] = 0
	}
	if err != nil || key.ID() != record.KeyID {
		return nil, fmt.Errorf("identity key ID does not match its material")
	}
	return key, nil
}

func requireJSONEOF(decoder *json.Decoder) error {
	var trailing json.RawMessage
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return fmt.Errorf("JSON contains a trailing value")
		}
		return err
	}
	return nil
}

func dependentActionStateExists(home string) (bool, error) {
	projects := filepath.Join(home, "projects")
	entries, err := boundedio.ReadDirNoSymlink(projects, 65536)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("inspect dependent action projects: %w", err)
	}
	for _, project := range entries {
		if !project.IsDir() || project.Type()&os.ModeSymlink != 0 {
			return false, fmt.Errorf("action project state contains an unexpected entry")
		}
		actionDir := filepath.Join(projects, project.Name(), "action")
		actionEntries, readErr := boundedio.ReadDirNoSymlink(actionDir, 256)
		if errors.Is(readErr, os.ErrNotExist) {
			continue
		}
		if readErr != nil {
			return false, fmt.Errorf("inspect dependent action state: %w", readErr)
		}
		for _, entry := range actionEntries {
			if entry.Name() != "state.lock" && entry.Name() != "ledger.lock" {
				return true, nil
			}
		}
	}
	return false, nil
}
