package agentsession

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const (
	maxStopTreeEntries       = 100_000
	maxStopTreeBytes         = stopPolicyContentHashBound
	stopGenerationMinBytes   = 16 << 20
	stopGenerationMinEntries = 1_024
)

func stopGenerationWorthwhile(repoRoot string, files []gitDirtyFile) bool {
	return stopGenerationWorthwhileWithMetrics(repoRoot, files, nil)
}

func stopGenerationWorthwhileWithMetrics(repoRoot string, files []gitDirtyFile, metrics *stopPolicyAttemptMetrics) bool {
	for _, file := range files {
		if strings.HasPrefix(file.WorktreeHash, "submodule:") {
			return false
		}
	}
	var bytes int64
	entries := 0
	for _, file := range files {
		if file.WorktreeHash == "missing" || strings.HasPrefix(file.WorktreeHash, "symlink:") {
			continue
		}
		path := filepath.Join(repoRoot, filepath.FromSlash(file.Path))
		info, err := os.Lstat(path)
		if err != nil {
			return false
		}
		if info.IsDir() {
			treeBytes, treeEntries, reached, ok := stopTreeWorkEstimateUntil(
				path, stopGenerationMinBytes-bytes, stopGenerationMinEntries-entries,
			)
			if metrics != nil {
				metrics.thresholdTreeEntries += treeEntries
			}
			if !ok {
				return false
			}
			bytes += treeBytes
			entries += treeEntries
			if reached {
				return true
			}
		} else if info.Mode().IsRegular() {
			bytes += info.Size()
			entries++
		}
		if bytes >= stopGenerationMinBytes || entries >= stopGenerationMinEntries {
			return true
		}
	}
	return false
}

var errStopGenerationThreshold = errors.New("stop generation threshold reached")

func stopTreeWorkEstimateUntil(root string, byteThreshold int64, entryThreshold int) (int64, int, bool, bool) {
	var bytes int64
	observedEntries := 0
	entries, err := walkStopTree(root, func(_ string, info os.FileInfo) error {
		observedEntries++
		if info.Mode().IsRegular() {
			bytes += info.Size()
		}
		if bytes >= byteThreshold || observedEntries >= entryThreshold {
			return errStopGenerationThreshold
		}
		return nil
	})
	if errors.Is(err, errStopGenerationThreshold) || entries >= entryThreshold {
		return bytes, entries, true, true
	}
	return bytes, entries, false, err == nil
}

func stopPathMetadataGeneration(path string, info os.FileInfo) (string, bool) {
	platform, ok := platformFileGeneration(path, info)
	if !ok {
		return "", false
	}
	return fmt.Sprintf(
		"mode=%s;size=%d;mtime=%d;platform=%s",
		info.Mode().String(),
		info.Size(),
		info.ModTime().UnixNano(),
		platform,
	), true
}

func stopWorktreeGeneration(repoRoot, path, indexEntry string) (string, bool) {
	fullPath := filepath.Join(repoRoot, filepath.FromSlash(path))
	info, err := os.Lstat(fullPath)
	if err != nil {
		return "missing", os.IsNotExist(err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		target, err := os.Readlink(fullPath)
		if err != nil {
			return "", false
		}
		metadata, ok := stopPathMetadataGeneration(fullPath, info)
		if !ok {
			return "", false
		}
		return "symlink:" + metadata + ":" + hashBytes([]byte(target)), true
	}
	if info.IsDir() {
		if strings.HasPrefix(indexEntry, "160000 ") {
			hash := submoduleWorktreeHash(fullPath)
			return hash, strings.HasPrefix(hash, "submodule:") && len(strings.TrimPrefix(hash, "submodule:")) == sha256.Size*2
		}
		return stopDirectoryGeneration(fullPath)
	}
	if !info.Mode().IsRegular() {
		return "", false
	}
	return stopPathMetadataGeneration(fullPath, info)
}

func stopDirectoryGeneration(root string) (string, bool) {
	hasher := sha256.New()
	_, err := walkStopTree(root, func(path string, info os.FileInfo) error {
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		generation, ok := stopPathMetadataGeneration(path, info)
		if !ok {
			return fmt.Errorf("platform identity unavailable for %s", rel)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			target, err := os.Readlink(path)
			if err != nil {
				return err
			}
			generation += ";target=" + hashBytes([]byte(target))
		}
		writeStopTreeComponent(hasher, filepath.ToSlash(rel))
		writeStopTreeComponent(hasher, generation)
		return nil
	})
	if err != nil {
		return "", false
	}
	return "dir-generation:" + hex.EncodeToString(hasher.Sum(nil)), true
}

func stopDirectoryContentHashObserved(root string, observe func(int64)) string {
	hasher := sha256.New()
	var totalBytes int64
	_, err := walkStopTree(root, func(path string, info os.FileInfo) error {
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		metadata, ok := stopPathMetadataGeneration(path, info)
		if !ok {
			return fmt.Errorf("platform identity unavailable for %s", filepath.ToSlash(rel))
		}
		writeStopTreeComponent(hasher, filepath.ToSlash(rel))
		writeStopTreeComponent(hasher, metadata)
		switch {
		case info.Mode()&os.ModeSymlink != 0:
			return fmt.Errorf("tree contains symlink %s", filepath.ToSlash(rel))
		case info.IsDir():
			writeStopTreeComponent(hasher, "directory")
		case info.Mode().IsRegular():
			totalBytes += info.Size()
			if totalBytes > maxStopTreeBytes {
				return fmt.Errorf("tree exceeds %d content bytes", maxStopTreeBytes)
			}
			contentHash, err := hashFileContentExpected(path, info)
			if err != nil {
				return err
			}
			if strings.HasPrefix(contentHash, "oversized:") {
				return fmt.Errorf("tree contains oversized file")
			}
			if observe != nil {
				observe(info.Size())
			}
			writeStopTreeComponent(hasher, contentHash)
		default:
			return fmt.Errorf("tree contains unsupported mode %s", info.Mode())
		}
		return nil
	})
	if err != nil {
		return "dir-error:" + err.Error()
	}
	return "dir:" + hex.EncodeToString(hasher.Sum(nil))
}

func stopPolicyDirectoryGeneration(root string) (string, bool) {
	hasher := sha256.New()
	_, err := walkStopTree(root, func(path string, info os.FileInfo) error {
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 || (!info.IsDir() && !info.Mode().IsRegular()) {
			return fmt.Errorf("unsupported policy-input mode %s at %s", info.Mode(), filepath.ToSlash(rel))
		}
		generation, ok := stopPathMetadataGeneration(path, info)
		if !ok {
			return fmt.Errorf("platform identity unavailable for %s", filepath.ToSlash(rel))
		}
		writeStopTreeComponent(hasher, filepath.ToSlash(rel))
		writeStopTreeComponent(hasher, generation)
		return nil
	})
	if err != nil {
		return "", false
	}
	return "policy-dir-generation:" + hex.EncodeToString(hasher.Sum(nil)), true
}

func walkStopTree(root string, visit func(string, os.FileInfo) error) (int, error) {
	info, err := os.Lstat(root)
	if err != nil {
		return 0, err
	}
	entries := 0
	if err := walkStopTreePath(root, info, &entries, visit); err != nil {
		return entries, err
	}
	return entries, nil
}

func walkStopTreePath(path string, info os.FileInfo, entries *int, visit func(string, os.FileInfo) error) error {
	*entries++
	if *entries > maxStopTreeEntries {
		return fmt.Errorf("tree exceeds %d entries", maxStopTreeEntries)
	}
	if err := visit(path, info); err != nil {
		return err
	}
	if !info.IsDir() {
		return nil
	}
	remaining := maxStopTreeEntries - *entries
	children, err := readStopDirectoryEntries(path, info, remaining)
	if err != nil {
		return err
	}
	for _, child := range children {
		childInfo, err := child.Info()
		if err != nil {
			return err
		}
		if err := walkStopTreePath(filepath.Join(path, child.Name()), childInfo, entries, visit); err != nil {
			return err
		}
	}
	return nil
}

func readStopDirectoryEntries(path string, expected os.FileInfo, limit int) ([]os.DirEntry, error) {
	directory, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	actual, statErr := directory.Stat()
	if statErr != nil || !actual.IsDir() || !os.SameFile(expected, actual) {
		if statErr == nil {
			statErr = fmt.Errorf("directory changed while scanning %s", path)
		}
		return nil, errors.Join(statErr, directory.Close())
	}
	entries := make([]os.DirEntry, 0, min(limit, 1_024))
	for {
		readLimit := min(1_024, limit+1-len(entries))
		if readLimit <= 0 {
			return nil, errors.Join(
				fmt.Errorf("tree exceeds %d entries", maxStopTreeEntries),
				directory.Close(),
			)
		}
		batch, readErr := directory.ReadDir(readLimit)
		entries = append(entries, batch...)
		if len(entries) > limit {
			return nil, errors.Join(
				fmt.Errorf("tree exceeds %d entries", maxStopTreeEntries),
				directory.Close(),
			)
		}
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			return nil, errors.Join(readErr, directory.Close())
		}
	}
	if err := directory.Close(); err != nil {
		return nil, err
	}
	sort.Slice(entries, func(left, right int) bool {
		return entries[left].Name() < entries[right].Name()
	})
	return entries, nil
}

func writeStopTreeComponent(hasher hash.Hash, value string) {
	var length [8]byte
	binary.LittleEndian.PutUint64(length[:], uint64(len(value)))
	_, _ = hasher.Write(length[:])
	_, _ = hasher.Write([]byte(value))
}
