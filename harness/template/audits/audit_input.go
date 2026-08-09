package main

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// readAuditFile is the common fail-closed boundary for repository files read
// by the portable audits. It refuses links and special files, caps allocation,
// and verifies that the opened object remains the path's current identity.
func readAuditFile(path string) ([]byte, error) {
	return readAuditCacheRegularFile(path, maxAuditCacheInputBytes, "audit input")
}

// readAuditDirectory bounds one directory and rejects a linked or replaced
// directory rather than returning a partial or externally redirected view.
func readAuditDirectory(path string) ([]os.DirEntry, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return nil, fmt.Errorf("audit input must be a non-symlink directory: %s", path)
	}
	return readAuditCacheDirectory(path, info)
}

// walkAuditTree is the bounded replacement for filepath.WalkDir. It preserves
// lexical ordering and SkipDir behavior without letting one huge directory be
// materialized before the global entry limit is checked.
func walkAuditTree(root string, visit fs.WalkDirFunc) error {
	info, err := os.Lstat(root)
	if err != nil {
		return visit(root, nil, err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return fmt.Errorf("audit tree root must be a non-symlink directory: %s", root)
	}
	visited := 0
	var walk func(string, fs.DirEntry) error
	walk = func(path string, entry fs.DirEntry) error {
		visited++
		if visited > maxAuditCacheDirectoryEntries {
			return fmt.Errorf("audit tree %s exceeds %d entries", root, maxAuditCacheDirectoryEntries)
		}
		if err := visit(path, entry, nil); err != nil {
			if err == filepath.SkipDir && entry.IsDir() {
				return nil
			}
			return err
		}
		if !entry.IsDir() {
			return nil
		}
		children, err := readAuditDirectory(path)
		if err != nil {
			return visit(path, entry, err)
		}
		for _, child := range children {
			if err := walk(filepath.Join(path, child.Name()), child); err != nil {
				return err
			}
		}
		return nil
	}
	return walk(root, fs.FileInfoToDirEntry(info))
}
