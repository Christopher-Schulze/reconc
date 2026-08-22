package impactlab

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"reconc.dev/reconc/internal/pathidentity"
)

const maxImpactSnapshotEntries = 100_000

type repositorySnapshot struct {
	root    string
	entries map[string]repositorySnapshotEntry
}

type repositorySnapshotEntry struct {
	info       os.FileInfo
	contentSHA string
	linkTarget string
}

func captureRepositorySnapshot(repoRoot string) (*repositorySnapshot, error) {
	root, err := pathidentity.ResolveExisting(repoRoot)
	if err != nil {
		return nil, err
	}
	info, err := os.Lstat(root)
	if err != nil {
		return nil, fmt.Errorf("inspect repository root: %w", err)
	}
	if !info.IsDir() {
		return nil, errors.New("repository root is not a directory")
	}
	snapshot := &repositorySnapshot{root: root, entries: make(map[string]repositorySnapshotEntry)}
	err = filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if relative == ".git" {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if len(snapshot.entries) >= maxImpactSnapshotEntries {
			return fmt.Errorf("repository snapshot exceeds %d entries", maxImpactSnapshotEntries)
		}
		captured, err := captureRepositorySnapshotEntry(path)
		if err != nil {
			return fmt.Errorf("capture %q: %w", filepath.ToSlash(relative), err)
		}
		snapshot.entries[filepath.ToSlash(relative)] = captured
		return nil
	})
	if err != nil {
		return nil, err
	}
	return snapshot, nil
}

func captureRepositorySnapshotEntry(path string) (repositorySnapshotEntry, error) {
	before, err := os.Lstat(path)
	if err != nil {
		return repositorySnapshotEntry{}, err
	}
	captured := repositorySnapshotEntry{info: before}
	switch {
	case before.Mode().IsRegular():
		captured.contentSHA, captured.info, err = stableFileSHA256(path, before)
	case before.Mode()&os.ModeSymlink != 0:
		captured.linkTarget, err = os.Readlink(path)
		if err == nil {
			var after os.FileInfo
			after, err = os.Lstat(path)
			if err == nil && !sameSnapshotMetadata(before, after) {
				err = errors.New("symbolic-link identity changed while capturing")
			}
		}
	}
	return captured, err
}

func stableFileSHA256(path string, before os.FileInfo) (digest string, info os.FileInfo, resultErr error) {
	file, err := os.Open(path)
	if err != nil {
		return "", nil, err
	}
	defer func() { resultErr = errors.Join(resultErr, file.Close()) }()
	opened, err := file.Stat()
	if err != nil || !opened.Mode().IsRegular() || !os.SameFile(before, opened) {
		return "", nil, errors.New("file identity changed while opening")
	}
	hash := sha256.New()
	if before.Size() == int64(^uint64(0)>>1) {
		return "", nil, errors.New("file size cannot be bounded safely")
	}
	written, copyErr := io.CopyN(hash, file, before.Size()+1)
	if copyErr != nil && !errors.Is(copyErr, io.EOF) {
		return "", nil, copyErr
	}
	after, err := os.Lstat(path)
	if err != nil || written != before.Size() || !sameSnapshotMetadata(opened, after) {
		return "", nil, errors.New("file identity or size changed while hashing")
	}
	return hex.EncodeToString(hash.Sum(nil)), opened, nil
}

func (s *repositorySnapshot) revalidate(repoRoot string) error {
	current, err := captureRepositorySnapshot(repoRoot)
	if err != nil {
		return err
	}
	if s == nil || current.root != s.root || len(current.entries) != len(s.entries) {
		return errors.New("repository filesystem identity changed")
	}
	for path, expected := range s.entries {
		observed, exists := current.entries[path]
		if !exists || !sameSnapshotMetadata(expected.info, observed.info) ||
			expected.contentSHA != observed.contentSHA || expected.linkTarget != observed.linkTarget {
			return fmt.Errorf("repository path %q changed", path)
		}
	}
	return nil
}

func sameSnapshotMetadata(left, right os.FileInfo) bool {
	return left != nil && right != nil && os.SameFile(left, right) &&
		left.Mode() == right.Mode() && left.Size() == right.Size() &&
		left.ModTime().Equal(right.ModTime())
}
