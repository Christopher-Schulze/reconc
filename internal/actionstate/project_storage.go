package actionstate

import (
	"fmt"
	"path/filepath"

	"reconc.dev/reconc/internal/retention"
)

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

func (s PrivateProjectStorage) privateFilePath(name string) (string, error) {
	if err := s.Validate(); err != nil {
		return "", err
	}
	if name == "" || filepath.Base(name) != name || name == "." || name == ".." {
		return "", fmt.Errorf("private project filename is invalid")
	}
	return filepath.Join(s.directory, name), nil
}
