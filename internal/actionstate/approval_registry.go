package actionstate

import (
	"fmt"
	"os"
	"path/filepath"

	"reconc.dev/reconc/internal/actionapproval"
	"reconc.dev/reconc/internal/pathidentity"
)

type LoadedApprovalRegistry struct {
	path     string
	identity string
	registry *actionapproval.CompiledRegistry
}

func (r LoadedApprovalRegistry) Path() string {
	return r.path
}

func (r LoadedApprovalRegistry) Identity() string {
	return r.identity
}

func (r LoadedApprovalRegistry) compiled() *actionapproval.CompiledRegistry {
	if r.path == "" || r.registry == nil || r.identity == "" || r.identity != r.registry.Identity() {
		return nil
	}
	return r.registry
}

func LoadApprovalAuthorityRegistry(path, repository string) (LoadedApprovalRegistry, error) {
	if path == "" || repository == "" {
		return LoadedApprovalRegistry{}, fmt.Errorf("approval authority registry path and repository are required")
	}
	original, err := filepath.Abs(path)
	if err != nil {
		return LoadedApprovalRegistry{}, fmt.Errorf("resolve approval authority registry path: %w", err)
	}
	info, err := os.Lstat(filepath.Clean(original))
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		if err == nil {
			err = fmt.Errorf("registry must be a non-symlink regular file")
		}
		return LoadedApprovalRegistry{}, fmt.Errorf("inspect approval authority registry: %w", err)
	}
	resolved, err := pathidentity.ResolveExisting(original)
	if err != nil {
		return LoadedApprovalRegistry{}, fmt.Errorf("resolve approval authority registry identity: %w", err)
	}
	repositoryIdentity, err := pathidentity.ResolveExisting(repository)
	if err != nil {
		return LoadedApprovalRegistry{}, fmt.Errorf("resolve approval authority repository identity: %w", err)
	}
	if pathContained(repositoryIdentity, resolved) {
		return LoadedApprovalRegistry{}, fmt.Errorf("approval authority registry must be outside the canonical repository")
	}
	if err := validatePrivateDirectory(filepath.Dir(resolved)); err != nil {
		return LoadedApprovalRegistry{}, fmt.Errorf("validate approval authority registry directory: %w", err)
	}
	body, err := readPrivateRegularFile(resolved, actionapproval.MaxAuthorityRegistryBytes)
	if err != nil {
		return LoadedApprovalRegistry{}, fmt.Errorf("read private approval authority registry: %w", err)
	}
	registry, err := actionapproval.DecodeRegistry(body)
	if err != nil {
		return LoadedApprovalRegistry{}, err
	}
	return LoadedApprovalRegistry{path: resolved, identity: registry.Identity(), registry: registry}, nil
}
