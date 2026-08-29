package actionstate

import (
	"fmt"
	"os"
	"path/filepath"

	"reconc.dev/reconc/internal/retention"
)

const privateJSONLSecurityIdentity = "reconc-private-project-jsonl-v1"

// PrivateProjectStorage is the narrow private filesystem and keyed-identity
// capability shared with Action Plane persistence owners. Values can only be
// obtained from a validated Store and never expose key material.
type PrivateProjectStorage struct {
	home               string
	repository         string
	repositoryIdentity string
	projectKey         string
	directory          string
	key                *IdentityKey
	keyLease           *IdentityKeyLease
}

func (s *Store) PrivateProjectStorage() (PrivateProjectStorage, error) {
	if s == nil || s.key == nil || s.keyLease == nil {
		return PrivateProjectStorage{}, fmt.Errorf("action state store is unavailable")
	}
	storage := PrivateProjectStorage{
		home: s.home, repository: s.repository, repositoryIdentity: s.repositoryIdentity,
		projectKey: s.projectKey, directory: s.directory, key: s.key, keyLease: s.keyLease,
	}
	if err := storage.Validate(); err != nil {
		return PrivateProjectStorage{}, err
	}
	return storage, nil
}

// OpenExistingPrivateProjectStorage resolves one existing project action
// directory without creating or repairing state. Repository and key identity
// must match the same observations used by OpenStore.
func OpenExistingPrivateProjectStorage(
	home, repository string,
	keyLease *IdentityKeyLease,
) (PrivateProjectStorage, error) {
	key, release, err := keyLease.acquireUse()
	if err != nil {
		return PrivateProjectStorage{}, fmt.Errorf("an active identity-key lease is required: %w", err)
	}
	defer release()
	resolvedHome, err := ResolveHome(home)
	if err != nil {
		return PrivateProjectStorage{}, err
	}
	resolvedRepository, repositoryIdentity, err := ObserveRepository(key, repository)
	if err != nil {
		return PrivateProjectStorage{}, err
	}
	if pathContained(resolvedRepository, resolvedHome) {
		return PrivateProjectStorage{}, fmt.Errorf("reconc action state must be outside the canonical repository")
	}
	projectDirectory := retention.ProjectDir(resolvedHome, resolvedRepository)
	projectKey := filepath.Base(projectDirectory)
	directory := filepath.Join(projectDirectory, "action")
	storage := PrivateProjectStorage{
		home: resolvedHome, repository: resolvedRepository,
		repositoryIdentity: repositoryIdentity, projectKey: projectKey,
		directory: directory, key: key, keyLease: keyLease,
	}
	if err := storage.Validate(); err != nil {
		return PrivateProjectStorage{}, err
	}
	return storage, nil
}

func (s PrivateProjectStorage) Validate() error {
	if s.home == "" || s.repository == "" || s.repositoryIdentity == "" || s.projectKey == "" ||
		s.directory == "" || s.key == nil || s.keyLease == nil {
		return fmt.Errorf("private project storage is incomplete")
	}
	key, release, err := s.keyLease.acquireUse()
	if err != nil {
		return fmt.Errorf("private project identity-key lease is inactive: %w", err)
	}
	defer release()
	if key != s.key || !identityUsesKey(s.repositoryIdentity, key.ID()) {
		return fmt.Errorf("private project identity binding is invalid")
	}
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
			return fmt.Errorf("validate private project storage %s: %w", path, err)
		}
	}
	return nil
}

func (s PrivateProjectStorage) ActionDirectory() string {
	return s.directory
}

func (s PrivateProjectStorage) RepositoryIdentity() string {
	return s.repositoryIdentity
}

func (s PrivateProjectStorage) ProjectKey() string {
	return s.projectKey
}

// WithIdentity holds the active key lease for the complete callback and
// exposes only the key's domain-separated identity operation.
func (s PrivateProjectStorage) WithIdentity(use func(*IdentityKey) error) error {
	if use == nil {
		return fmt.Errorf("identity callback is required")
	}
	if err := s.Validate(); err != nil {
		return err
	}
	key, release, err := s.keyLease.acquireUse()
	if err != nil {
		return err
	}
	defer release()
	if key != s.key {
		return fmt.Errorf("private project identity key changed")
	}
	return use(key)
}

func (s PrivateProjectStorage) ReadPrivateFile(name string, maximum int64) ([]byte, error) {
	path, err := s.privateFilePath(name)
	if err != nil {
		return nil, err
	}
	return readPrivateRegularFile(path, maximum)
}

func (s PrivateProjectStorage) PublishPrivateFile(name string, body []byte) error {
	path, err := s.privateFilePath(name)
	if err != nil {
		return err
	}
	return publishPrivateFile(path, body)
}

// JSONLSecurityIdentity binds durable JSONL journals to Reconc's private-file
// contract instead of treating modes alone as sufficient on Windows.
func (s PrivateProjectStorage) JSONLSecurityIdentity() string {
	return privateJSONLSecurityIdentity
}

// ValidateJSONLDirectory verifies that JSONL state remains in this exact
// validated private project directory.
func (s PrivateProjectStorage) ValidateJSONLDirectory(path string) error {
	if err := s.Validate(); err != nil {
		return err
	}
	if filepath.Clean(path) != path || path != s.directory {
		return fmt.Errorf("JSONL directory is outside private project storage")
	}
	return nil
}

// SecureJSONLFile applies the private-file contract to a newly created JSONL
// object before or immediately after publication. Existing files are validated
// without repair.
func (s PrivateProjectStorage) SecureJSONLFile(path string) error {
	if err := s.validateJSONLFilePath(path); err != nil {
		return err
	}
	return securePublishedPrivateFile(path)
}

// ValidateJSONLFile verifies one bounded private JSONL file without exposing
// its contents or repairing an existing unsafe object.
func (s PrivateProjectStorage) ValidateJSONLFile(path string, maximum int64) error {
	if err := s.validateJSONLFilePath(path); err != nil {
		return err
	}
	return validatePrivateRegularFile(path, maximum)
}

// ValidateOpenedJSONLFile validates one already opened private JSONL file.
// The caller retains ownership of the descriptor and must close it.
func (s PrivateProjectStorage) ValidateOpenedJSONLFile(file *os.File, info os.FileInfo, maximum int64) error {
	if file == nil || info == nil {
		return fmt.Errorf("private JSONL file handle is unavailable")
	}
	if maximum <= 0 {
		return fmt.Errorf("private JSONL file maximum must be positive")
	}
	if info.Size() > maximum {
		return fmt.Errorf("private JSONL file exceeds %d bytes", maximum)
	}
	if err := s.validateJSONLFilePath(file.Name()); err != nil {
		return err
	}
	return validatePrivateFile(file, info)
}

func (s PrivateProjectStorage) validateJSONLFilePath(path string) error {
	if filepath.Clean(path) != path {
		return fmt.Errorf("JSONL file path is not clean")
	}
	expected, err := s.privateFilePath(filepath.Base(path))
	if err != nil {
		return err
	}
	if path != expected {
		return fmt.Errorf("JSONL file is outside private project storage")
	}
	return nil
}

func (s PrivateProjectStorage) privateFilePath(name string) (string, error) {
	if err := s.Validate(); err != nil {
		return "", err
	}
	if name == "" || filepath.Base(name) != name || name == "." || name == ".." {
		return "", fmt.Errorf("private project filename is invalid")
	}
	return filepath.Join(s.directory, name), nil
}
