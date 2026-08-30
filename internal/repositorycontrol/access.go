// Package repositorycontrol owns the access contract for repository-local
// Reconc control artifacts.
package repositorycontrol

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

const (
	RootName = ".reconc"
	RunName  = "run"
)

const PublicDirectoryMode os.FileMode = 0o755

// EnsureRoot creates a missing .reconc directory with deterministic public
// repository-artifact access. Existing directories are identity-validated and
// preserved byte-for-byte, including stricter modes and shared-repository ACLs.
func EnsureRoot(repoRoot string) error {
	repository, err := os.OpenRoot(repoRoot)
	if err != nil {
		return fmt.Errorf("open repository control root: %w", err)
	}
	_, _, ensureErr := EnsurePublicDirectory(repository, RootName)
	return errors.Join(ensureErr, repository.Close())
}

// EnsurePublicDirectory creates one direct child of parent with deterministic
// public access and returns its stable opened identity. Existing directories
// are never chmodded or assigned a replacement ACL.
func EnsurePublicDirectory(parent *os.Root, name string) (resultInfo os.FileInfo, created bool, resultErr error) {
	return ensureDirectory(parent, name, PublicDirectoryMode)
}

// EnsureInheritedDirectory creates a public repository-control child with the
// access class of its parent. This keeps administrator-selected shared-repo or
// single-owner boundaries intact for coordination subdirectories.
func EnsureInheritedDirectory(parent *os.Root, name string) (os.FileInfo, bool, error) {
	if parent == nil {
		return nil, false, errors.New("repository control parent is required")
	}
	parentInfo, err := parent.Stat(".")
	if err != nil {
		return nil, false, fmt.Errorf("inspect repository control parent: %w", err)
	}
	return ensureDirectory(parent, name, inheritedDirectoryMode(parentInfo))
}

func ensureDirectory(parent *os.Root, name string, createMode os.FileMode) (resultInfo os.FileInfo, created bool, resultErr error) {
	if parent == nil || !filepath.IsLocal(name) || filepath.Base(name) != name || name == "." {
		return nil, false, errors.New("repository control directory name must be one local component")
	}
	if err := parent.Mkdir(name, createMode.Perm()); err == nil {
		created = true
	} else if !errors.Is(err, os.ErrExist) {
		return nil, false, fmt.Errorf("create repository control directory %s: %w", name, err)
	}
	before, err := parent.Lstat(name)
	if err != nil || before.Mode()&os.ModeSymlink != 0 || !before.IsDir() {
		return nil, created, errors.Join(fmt.Errorf("repository control path must be a non-symlink directory: %s", name), err)
	}
	directory, err := parent.OpenRoot(name)
	if err != nil {
		return nil, created, fmt.Errorf("open repository control directory %s: %w", name, err)
	}
	defer func() { resultErr = errors.Join(resultErr, directory.Close()) }()
	if created {
		if err := secureCreatedPublicDirectory(directory, createMode); err != nil {
			return nil, true, fmt.Errorf("secure created repository control directory %s: %w", name, err)
		}
	}
	opened, statErr := directory.Stat(".")
	current, lstatErr := parent.Lstat(name)
	if statErr != nil || lstatErr != nil || opened == nil || current == nil ||
		!opened.IsDir() || current.Mode()&os.ModeSymlink != 0 || !current.IsDir() ||
		!os.SameFile(before, opened) || !os.SameFile(opened, current) {
		return nil, created, errors.Join(fmt.Errorf("repository control directory %s changed identity while opening", name), statErr, lstatErr)
	}
	return opened, created, nil
}

// CoordinationFileMode derives a new lock file's access from its directory.
// Existing files are never repaired to this mode.
func CoordinationFileMode(directory os.FileInfo) os.FileMode {
	return coordinationFileMode(directory)
}

// ValidateRoot validates an existing .reconc path without changing its mode,
// ownership, ACL, contents, or identity.
func ValidateRoot(path string) error {
	before, err := os.Lstat(path)
	if err != nil || before.Mode()&os.ModeSymlink != 0 || !before.IsDir() {
		return errors.Join(fmt.Errorf("repository control root must be a non-symlink directory: %s", path), err)
	}
	directory, err := os.OpenRoot(path)
	if err != nil {
		return fmt.Errorf("open repository control root %s: %w", path, err)
	}
	opened, statErr := directory.Stat(".")
	current, lstatErr := os.Lstat(path)
	closeErr := directory.Close()
	if statErr != nil || lstatErr != nil || opened == nil || current == nil ||
		!opened.IsDir() || current.Mode()&os.ModeSymlink != 0 || !current.IsDir() ||
		!os.SameFile(before, opened) || !os.SameFile(opened, current) {
		return errors.Join(fmt.Errorf("repository control root changed identity: %s", path), statErr, lstatErr, closeErr)
	}
	return closeErr
}
