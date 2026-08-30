package jsonl

import (
	"context"
	"errors"
	"fmt"
	"os"
)

func Enforce(path string, policy Policy) (EnforceResult, error) {
	return EnforceContext(context.Background(), path, policy)
}

// EnforceContext applies retention under the caller lifecycle and the default
// lock timeout.
func EnforceContext(ctx context.Context, path string, policy Policy) (EnforceResult, error) {
	return EnforceContextWithLayout(ctx, path, policy, defaultLayout(path))
}

// EnforceWithLayout is Enforce with the same auxiliary layout used by the
// writer.
func EnforceWithLayout(path string, policy Policy, layout Layout) (EnforceResult, error) {
	return EnforceContextWithLayout(context.Background(), path, policy, layout)
}

// EnforceContextWithLayout is EnforceWithLayout under the caller lifecycle.
func EnforceContextWithLayout(ctx context.Context, path string, policy Policy, layout Layout) (EnforceResult, error) {
	if ctx == nil {
		return EnforceResult{}, errors.New("jsonl enforcement context is required")
	}
	if err := ctx.Err(); err != nil {
		return EnforceResult{}, err
	}
	if err := validatePolicy(policy); err != nil {
		return EnforceResult{}, err
	}
	if err := validateLayout(path, layout); err != nil {
		return EnforceResult{}, err
	}
	result := EnforceResult{}
	err := withLayoutLockLeaseContext(ctx, path, layout, true, func(lockedLayout Layout) error {
		if err := recoverAppendLockedWithLayout(path, lockedLayout, nil); err != nil {
			return err
		}
		candidates, err := archiveCandidatesContext(ctx, path)
		if err != nil {
			return err
		}
		for _, candidate := range candidates {
			if candidate.index > policy.MaxArchives {
				info, err := os.Lstat(candidate.path)
				if err != nil && !errors.Is(err, os.ErrNotExist) {
					return err
				}
				if err == nil {
					if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
						return fmt.Errorf("JSONL archive must be a non-symlink regular file: %s", candidate.path)
					}
					if err := removeJSONLPathWithLayout(candidate.path, lockedLayout); err != nil {
						return err
					}
					result.BytesFreed += info.Size()
					result.FilesRemoved++
				}
			}
		}
		for index := policy.MaxArchives; index >= 0; index-- {
			if err := ctx.Err(); err != nil {
				return err
			}
			candidate := path
			if index > 0 {
				candidate = fmt.Sprintf("%s.%d", path, index)
			}
			freed, err := trimTailWithLayout(path, candidate, policy.MaxBytes, lockedLayout)
			if err != nil {
				return err
			}
			result.BytesFreed += freed
		}
		return nil
	})
	return result, err
}

// PathsOldestFirst returns existing bounded-ring files in chronological
// order, then the live file. Readers use this to preserve append order.
func PathsOldestFirst(path string, maxArchives int) ([]string, error) {
	return PathsOldestFirstContext(context.Background(), path, maxArchives)
}

// PathsOldestFirstContext is PathsOldestFirst under the caller lifecycle.
func PathsOldestFirstContext(ctx context.Context, path string, maxArchives int) ([]string, error) {
	if ctx == nil {
		return nil, errors.New("jsonl ring context is required")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := validateArchiveBound(maxArchives); err != nil {
		return nil, err
	}
	candidates, err := archiveCandidatesContext(ctx, path)
	if err != nil {
		return nil, err
	}
	for _, candidate := range candidates {
		if candidate.index > maxArchives {
			return nil, fmt.Errorf("JSONL archive index %d exceeds bound %d: %s", candidate.index, maxArchives, candidate.path)
		}
	}
	paths := make([]string, 0, maxArchives+1)
	for index := maxArchives; index >= 1; index-- {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		candidate := fmt.Sprintf("%s.%d", path, index)
		if info, err := os.Lstat(candidate); err == nil {
			if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
				return nil, fmt.Errorf("JSONL archive must be a non-symlink regular file: %s", candidate)
			}
			paths = append(paths, candidate)
		} else if !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
	}
	if info, err := os.Lstat(path); err == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return nil, fmt.Errorf("JSONL live path must be a non-symlink regular file: %s", path)
		}
		paths = append(paths, path)
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	return paths, nil
}

// RingSize returns the bytes and file count owned by one bounded JSONL ring.
// It uses lstat semantics so a link or special file cannot contribute target
// bytes to a cleanup decision. The bound includes the live file and archives
// through maxArchives.
func RingSize(path string, maxArchives int) (int64, int, error) {
	return RingSizeContext(context.Background(), path, maxArchives)
}

// RingSizeContext is RingSize under the caller lifecycle.
func RingSizeContext(ctx context.Context, path string, maxArchives int) (int64, int, error) {
	paths, err := PathsOldestFirstContext(ctx, path, maxArchives)
	if err != nil {
		return 0, 0, err
	}
	var bytes int64
	files := 0
	for _, candidate := range paths {
		if err := ctx.Err(); err != nil {
			return bytes, files, err
		}
		info, err := os.Lstat(candidate)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return bytes, files, err
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return bytes, files, fmt.Errorf("JSONL ring member must be a non-symlink regular file: %s", candidate)
		}
		bytes += info.Size()
		files++
	}
	return bytes, files, nil
}

func validateArchiveBound(maxArchives int) error {
	if maxArchives < 0 || maxArchives > MaxArchiveFiles {
		return fmt.Errorf("jsonl maxArchives must be between 0 and %d", MaxArchiveFiles)
	}
	return nil
}

func validatePolicy(policy Policy) error {
	if policy.MaxBytes <= 0 {
		return errors.New("jsonl MaxBytes must be positive")
	}
	return validateArchiveBound(policy.MaxArchives)
}

// RemoveArchiveWithLayout removes one discovered canonical archive while the
// caller holds the lease supplied by WithLayoutMaintenanceContext. The exact
// discovered identity must still own the archive name.
func RemoveArchiveWithLayout(path string, index int, expected os.FileInfo, layout Layout) (int64, error) {
	if index < 1 || index > MaxArchiveFiles {
		return 0, fmt.Errorf("jsonl archive index must be between 1 and %d", MaxArchiveFiles)
	}
	if expected == nil {
		return 0, errors.New("jsonl archive removal requires a discovered identity")
	}
	if layout.lockLease == nil {
		return 0, errors.New("jsonl archive removal requires an active maintenance lease")
	}
	if err := layout.validateLockLease(); err != nil {
		return 0, err
	}
	archive := archivePath(path, index)
	if err := validateExistingLayoutFileMode(path, layout, archive, expected.Mode(), layout.FileMode); err != nil {
		return 0, err
	}
	parent, err := openJSONLParentWithLayout(archive, layout)
	if err != nil {
		return 0, err
	}
	removed, removeErr := parent.removeIfSame(parent.name, expected)
	closeErr := parent.close()
	if !removed {
		return 0, errors.Join(removeErr, closeErr)
	}
	return expected.Size(), errors.Join(removeErr, closeErr)
}
