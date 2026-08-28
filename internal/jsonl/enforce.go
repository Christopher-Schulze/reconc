package jsonl

import (
	"errors"
	"fmt"
	"os"
)

func Enforce(path string, policy Policy) (EnforceResult, error) {
	return EnforceWithLayout(path, policy, defaultLayout(path))
}

// EnforceWithLayout is Enforce with the same auxiliary layout used by the
// writer.
func EnforceWithLayout(path string, policy Policy, layout Layout) (EnforceResult, error) {
	if err := validatePolicy(policy); err != nil {
		return EnforceResult{}, err
	}
	if err := validateLayout(path, layout); err != nil {
		return EnforceResult{}, err
	}
	result := EnforceResult{}
	err := withLayoutLock(path, layout, func() error {
		if err := recoverAppendLockedWithLayout(path, layout, nil); err != nil {
			return err
		}
		candidates, err := archiveCandidates(path)
		if err != nil {
			return err
		}
		for _, candidate := range candidates {
			if candidate.index > policy.MaxArchives {
				info, err := os.Stat(candidate.path)
				if err != nil && !errors.Is(err, os.ErrNotExist) {
					return err
				}
				if err == nil {
					if err := removeJSONLPath(candidate.path); err != nil {
						return err
					}
					result.BytesFreed += info.Size()
					result.FilesRemoved++
				}
			}
		}
		for index := policy.MaxArchives; index >= 0; index-- {
			candidate := path
			if index > 0 {
				candidate = fmt.Sprintf("%s.%d", path, index)
			}
			freed, err := trimTailWithLayout(candidate, policy.MaxBytes, layout)
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
	if maxArchives < 0 || maxArchives > 32 {
		return nil, errors.New("jsonl maxArchives must be between 0 and 32")
	}
	paths := make([]string, 0, maxArchives+1)
	for index := maxArchives; index >= 1; index-- {
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

func validatePolicy(policy Policy) error {
	if policy.MaxBytes <= 0 {
		return errors.New("jsonl MaxBytes must be positive")
	}
	if policy.MaxArchives < 0 || policy.MaxArchives > 32 {
		return errors.New("jsonl MaxArchives must be between 0 and 32")
	}
	return nil
}
