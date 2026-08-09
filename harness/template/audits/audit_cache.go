package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

// cacheVersion is bumped whenever audit logic changes in a way that should
// invalidate every cached pass. The cache key embeds this constant so a
// stale binary cannot return a false pass after the rules tightened.
const cacheVersion = "v8-2026-08-08"

const (
	maxAuditCacheInputBytes       = 64 << 20
	maxAuditCacheStateBytes       = 8 << 20
	maxAuditCacheDirectoryEntries = 100_000
)

const (
	cacheRel     = ".reconc/cache/audit-results.json"
	cacheEnv     = "RECONC_AUDIT_NO_CACHE"
	cachePassTag = "pass"
)

var (
	auditCacheMu       sync.Mutex
	auditCacheKeyLocks sync.Map
)

type cacheEntry struct {
	Hash    string `json:"hash"`
	Result  string `json:"result"`
	Version string `json:"version"`
}

type cacheFile struct {
	Entries map[string]cacheEntry `json:"entries"`
}

// fileGlobs is a sorted set of file paths that fully define a sub-audit's
// input. The cache hashes them in lexical order so identical content always
// produces the same digest. Globs that read directories return their
// recursive file list; globs that read individual files just return that file.
type cacheInputs struct {
	files          []string
	structurePaths []string
	metadataPaths  []string
	values         []string
	inputErrors    []error
}

func newCacheInputs() *cacheInputs {
	return &cacheInputs{}
}

// AddFile appends a declared file path. Missing and unreadable paths remain
// explicit inputs so their later creation or accessibility changes the key.
func (c *cacheInputs) AddFile(path string) {
	c.files = append(c.files, path)
}

// AddPathMetadata records one path without reading its contents. Directory
// metadata invalidates caches on child add/remove/rename while avoiding a
// recursive archive read on every hot-path audit.
func (c *cacheInputs) AddPathMetadata(path string) {
	c.metadataPaths = append(c.metadataPaths, path)
}

// AddValue adds a deterministic non-file input such as a Git tree object ID.
func (c *cacheInputs) AddValue(name, value string) {
	c.values = append(c.values, name+"\x00"+value)
}

// AddTreeStructure appends every regular file under root with one of the
// suffixes BUT marks them so Hash() includes only their paths and existence,
// not their contents. Used by audits whose result depends on the directory
// tree shape (e.g. test-coverage: "does each Go dir have a *_test.go?") so
// pure content edits do not invalidate the cache.
func (c *cacheInputs) AddTreeStructure(root string, suffixes []string) {
	err := walkAuditTree(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			c.metadataPaths = append(c.metadataPaths, path)
			return nil
		}
		if len(suffixes) == 0 {
			c.structurePaths = append(c.structurePaths, path)
			return nil
		}
		for _, suffix := range suffixes {
			if strings.HasSuffix(path, suffix) {
				c.structurePaths = append(c.structurePaths, path)
				return nil
			}
		}
		return nil
	})
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			err = validateAbsentCachePath(root)
		}
		if err != nil {
			c.inputErrors = append(c.inputErrors, fmt.Errorf("walk tree structure %s: %w", root, err))
		}
	}
}

// AddTree appends every regular file under root with one of the suffixes.
// Returns silently if root does not exist.
func (c *cacheInputs) AddTree(root string, suffixes []string) {
	err := walkAuditTree(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			c.metadataPaths = append(c.metadataPaths, path)
			return nil
		}
		if len(suffixes) == 0 {
			c.files = append(c.files, path)
			return nil
		}
		for _, suffix := range suffixes {
			if strings.HasSuffix(path, suffix) {
				c.files = append(c.files, path)
				return nil
			}
		}
		return nil
	})
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			err = validateAbsentCachePath(root)
		}
		if err != nil {
			c.inputErrors = append(c.inputErrors, fmt.Errorf("walk tree %s: %w", root, err))
		}
	}
}

// validateAbsentCachePath distinguishes a genuinely absent path from a path
// that only appears absent because an ancestor is not a usable directory.
func validateAbsentCachePath(path string) error {
	ancestor := filepath.Dir(filepath.Clean(path))
	for {
		info, err := os.Lstat(ancestor)
		if err == nil {
			if info.Mode()&os.ModeSymlink != 0 {
				target, statErr := os.Stat(ancestor)
				if statErr != nil {
					return fmt.Errorf("inspect ancestor %s: %w", ancestor, statErr)
				}
				info = target
			}
			if !info.IsDir() {
				return fmt.Errorf("ancestor %s is not a directory", ancestor)
			}
			return nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("inspect ancestor %s: %w", ancestor, err)
		}
		parent := filepath.Dir(ancestor)
		if parent == ancestor {
			return fmt.Errorf("no existing directory ancestor for %s", path)
		}
		ancestor = parent
	}
}

// Hash returns a deterministic SHA256 over the cache version, the sorted file
// list, and each file's content. Missing files use a canonical absent marker;
// every other read or metadata failure aborts hashing so no partial tree can
// reuse a cached pass.
// Structure paths (added via AddTreeStructure) contribute only their path
// and existence, not their content, so content-only edits do not invalidate
// audits whose result depends only on the directory tree shape.
func (c *cacheInputs) Hash() (string, error) {
	if len(c.inputErrors) > 0 {
		return "", errors.Join(c.inputErrors...)
	}
	sort.Strings(c.files)
	sort.Strings(c.structurePaths)
	sort.Strings(c.metadataPaths)
	sort.Strings(c.values)
	digest := sha256.New()
	digest.Write([]byte(cacheVersion))
	digest.Write([]byte{0})
	for _, path := range c.files {
		digest.Write([]byte(path))
		digest.Write([]byte{0})
		content, err := readAuditCacheInput(path)
		if err != nil {
			if !errors.Is(err, os.ErrNotExist) {
				return "", fmt.Errorf("hash read %s: %w", path, err)
			}
			if err := validateAbsentCachePath(path); err != nil {
				return "", fmt.Errorf("hash read %s: %w", path, err)
			}
			digest.Write([]byte("ABSENT"))
		} else {
			digest.Write(content)
		}
		digest.Write([]byte{0})
	}
	digest.Write([]byte("STRUCTURE"))
	digest.Write([]byte{0})
	for _, path := range c.structurePaths {
		digest.Write([]byte(path))
		digest.Write([]byte{0})
		if _, err := os.Stat(path); err == nil {
			digest.Write([]byte("PRESENT"))
		} else if errors.Is(err, os.ErrNotExist) {
			if err := validateAbsentCachePath(path); err != nil {
				return "", fmt.Errorf("hash structure metadata %s: %w", path, err)
			}
			digest.Write([]byte("ABSENT"))
		} else {
			return "", fmt.Errorf("hash structure metadata %s: %w", path, err)
		}
		digest.Write([]byte{0})
	}
	digest.Write([]byte("METADATA"))
	digest.Write([]byte{0})
	for _, path := range c.metadataPaths {
		digest.Write([]byte(path))
		digest.Write([]byte{0})
		info, err := os.Lstat(path)
		if err != nil {
			if !errors.Is(err, os.ErrNotExist) {
				return "", fmt.Errorf("hash metadata %s: %w", path, err)
			}
			if err := validateAbsentCachePath(path); err != nil {
				return "", fmt.Errorf("hash metadata %s: %w", path, err)
			}
			digest.Write([]byte("ABSENT"))
		} else {
			fmt.Fprintf(digest, "%s\x00%d\x00%d", info.Mode().String(), info.Size(), info.ModTime().UnixNano())
			if info.IsDir() {
				entries, readErr := readAuditCacheDirectory(path, info)
				if readErr != nil {
					return "", fmt.Errorf("hash metadata %s: %w", path, readErr)
				}
				for _, entry := range entries {
					digest.Write([]byte{0})
					digest.Write([]byte(entry.Name()))
					digest.Write([]byte{0})
					digest.Write([]byte(entry.Type().String()))
				}
			}
		}
		digest.Write([]byte{0})
	}
	digest.Write([]byte("VALUES"))
	digest.Write([]byte{0})
	for _, value := range c.values {
		digest.Write([]byte(value))
		digest.Write([]byte{0})
	}
	return hex.EncodeToString(digest.Sum(nil)), nil
}

func readAuditCacheInput(path string) ([]byte, error) {
	return readAuditCacheRegularFile(path, maxAuditCacheInputBytes, "cache input")
}

func readAuditCacheRegularFile(path string, maxBytes int64, label string) ([]byte, error) {
	before, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if before.Mode()&os.ModeSymlink != 0 || !before.Mode().IsRegular() {
		return nil, fmt.Errorf("%s must be a non-symlink regular file", label)
	}
	if before.Size() > maxBytes {
		return nil, fmt.Errorf("%s exceeds %d bytes", label, maxBytes)
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	opened, statErr := file.Stat()
	afterOpen, lstatErr := os.Lstat(path)
	if statErr != nil || lstatErr != nil || !opened.Mode().IsRegular() ||
		!os.SameFile(before, opened) || !os.SameFile(opened, afterOpen) {
		if statErr == nil && lstatErr == nil {
			statErr = fmt.Errorf("cache input changed identity while opening")
		}
		return nil, errors.Join(statErr, lstatErr, file.Close())
	}
	body, readErr := io.ReadAll(io.LimitReader(file, maxBytes+1))
	afterRead, statErr := file.Stat()
	afterPath, lstatErr := os.Lstat(path)
	closeErr := file.Close()
	if err := errors.Join(readErr, statErr, lstatErr, closeErr); err != nil {
		return nil, err
	}
	if int64(len(body)) > maxBytes {
		return nil, fmt.Errorf("%s exceeds %d bytes", label, maxBytes)
	}
	if !os.SameFile(opened, afterRead) || !os.SameFile(afterRead, afterPath) ||
		opened.Size() != afterRead.Size() || !opened.ModTime().Equal(afterRead.ModTime()) {
		return nil, fmt.Errorf("cache input changed while reading")
	}
	return body, nil
}

func readAuditCacheDirectory(path string, expected os.FileInfo) ([]os.DirEntry, error) {
	directory, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	opened, statErr := directory.Stat()
	if statErr != nil || !opened.IsDir() || !os.SameFile(expected, opened) {
		if statErr == nil {
			statErr = fmt.Errorf("cache metadata directory changed while opening")
		}
		return nil, errors.Join(statErr, directory.Close())
	}
	entries, readErr := directory.ReadDir(maxAuditCacheDirectoryEntries + 1)
	after, lstatErr := os.Lstat(path)
	closeErr := directory.Close()
	if readErr != nil && !errors.Is(readErr, io.EOF) {
		return nil, errors.Join(readErr, lstatErr, closeErr)
	}
	if err := errors.Join(lstatErr, closeErr); err != nil {
		return nil, err
	}
	if !os.SameFile(opened, after) {
		return nil, fmt.Errorf("cache metadata directory changed while reading")
	}
	if opened.Mode() != after.Mode() || opened.Size() != after.Size() ||
		!opened.ModTime().Equal(after.ModTime()) {
		return nil, fmt.Errorf("cache metadata directory metadata changed while reading")
	}
	if len(entries) > maxAuditCacheDirectoryEntries {
		return nil, fmt.Errorf("cache metadata directory exceeds %d entries", maxAuditCacheDirectoryEntries)
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	return entries, nil
}

// runWithCache executes fn unless the inputs already hashed to a previously
// passing result for this audit name. On pass it persists the new entry; on
// failure it removes the cache entry so the next call re-runs the audit and
// the user does not silently keep the stale pass.
func runWithCache(root string, name string, inputs *cacheInputs, fn func() []string) []string {
	if os.Getenv(cacheEnv) != "" {
		return fn()
	}
	hash, err := inputs.Hash()
	if err != nil {
		return []string{fmt.Sprintf("audit %s cache input failed: %v", name, err)}
	}
	keyLock := auditCacheKeyLock(root, name)
	keyLock.Lock()
	defer keyLock.Unlock()

	cachePath := filepath.Join(root, filepath.FromSlash(cacheRel))
	var result []string
	err = withAuditCacheNamedLock(root, auditCacheKeyLockPath(root, name), func() error {
		if auditCacheHasPass(root, cachePath, name, hash) {
			verifiedHash, verifyErr := inputs.Hash()
			if verifyErr != nil {
				result = append(fn(), fmt.Sprintf("audit %s cache input failed after cache lookup: %v", name, verifyErr))
				return nil
			}
			if verifiedHash == hash {
				return nil
			}
			result = append(fn(), fmt.Sprintf("audit %s cache input changed during cache lookup", name))
			return nil
		}
		result = fn()
		verifiedHash, verifyErr := inputs.Hash()
		if verifyErr != nil {
			result = append(result, fmt.Sprintf("audit %s cache input failed after evaluation: %v", name, verifyErr))
			return nil
		}
		if verifiedHash != hash {
			result = append(result, fmt.Sprintf("audit %s cache input changed during evaluation", name))
			return nil
		}
		publishAuditCacheResult(root, cachePath, name, hash, result)
		return nil
	})
	if err != nil {
		return fn()
	}
	return result
}

func auditCacheHasPass(root, cachePath, name, hash string) bool {
	auditCacheMu.Lock()
	defer auditCacheMu.Unlock()
	hit := false
	err := withAuditCacheNamedLock(root, cachePath+".lock", func() error {
		cache := loadCacheFile(cachePath)
		entry, ok := cache.Entries[name]
		hit = ok && entry.Version == cacheVersion && entry.Result == cachePassTag && entry.Hash == hash
		return nil
	})
	return err == nil && hit
}

func publishAuditCacheResult(root, cachePath, name, hash string, result []string) {
	auditCacheMu.Lock()
	defer auditCacheMu.Unlock()
	_ = withAuditCacheNamedLock(root, cachePath+".lock", func() error {
		cache := loadCacheFile(cachePath)
		if len(result) == 0 {
			cache.Entries[name] = cacheEntry{Hash: hash, Result: cachePassTag, Version: cacheVersion}
		} else {
			delete(cache.Entries, name)
		}
		_ = saveCacheFile(root, cachePath, cache)
		return nil
	})
}

func auditCacheKeyLock(root, name string) *sync.Mutex {
	key := filepath.Clean(root) + "\x00" + name
	lock, _ := auditCacheKeyLocks.LoadOrStore(key, &sync.Mutex{})
	return lock.(*sync.Mutex)
}

func auditCacheKeyLockPath(root, name string) string {
	digest := sha256.Sum256([]byte(name))
	filename := "audit-key-" + hex.EncodeToString(digest[:]) + ".lock"
	return filepath.Join(root, ".reconc", "cache", filename)
}

func loadCacheFile(path string) cacheFile {
	cache := cacheFile{Entries: map[string]cacheEntry{}}
	bytes, err := readAuditCacheRegularFile(path, maxAuditCacheStateBytes, "cache state")
	if err != nil {
		return cache
	}
	if err := json.Unmarshal(bytes, &cache); err != nil {
		return cacheFile{Entries: map[string]cacheEntry{}}
	}
	if cache.Entries == nil {
		cache.Entries = map[string]cacheEntry{}
	}
	return cache
}

func saveCacheFile(root, path string, cache cacheFile) bool {
	if err := ensureAuditCacheDirectory(root); err != nil {
		return false
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".audit-cache-*")
	if err != nil {
		return false
	}
	defer os.Remove(tmp.Name())
	encoder := json.NewEncoder(tmp)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(cache); err != nil {
		tmp.Close()
		return false
	}
	if err := tmp.Close(); err != nil {
		return false
	}
	if err := ensureAuditCacheDirectory(root); err != nil {
		return false
	}
	if info, err := os.Lstat(path); err == nil && (info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular()) {
		return false
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return false
	}
	return os.Rename(tmp.Name(), path) == nil
}

func withAuditCacheNamedLock(root, lockPath string, fn func() error) (err error) {
	if err := ensureAuditCacheDirectory(root); err != nil {
		return err
	}
	before, beforeErr := os.Lstat(lockPath)
	if beforeErr == nil && (before.Mode()&os.ModeSymlink != 0 || !before.Mode().IsRegular()) {
		return fmt.Errorf("audit cache lock must be a non-symlink regular file: %s", lockPath)
	}
	if beforeErr != nil && !errors.Is(beforeErr, os.ErrNotExist) {
		return beforeErr
	}
	file, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	opened, statErr := file.Stat()
	after, lstatErr := os.Lstat(lockPath)
	parentErr := ensureAuditCacheDirectory(root)
	identityChanged := beforeErr == nil && statErr == nil && !os.SameFile(before, opened)
	if statErr != nil || lstatErr != nil || parentErr != nil || identityChanged ||
		!opened.Mode().IsRegular() || !os.SameFile(opened, after) {
		if statErr == nil && lstatErr == nil {
			statErr = fmt.Errorf("audit cache lock changed identity while opening: %s", lockPath)
		}
		return errors.Join(statErr, lstatErr, parentErr, file.Close())
	}
	unlock, err := lockAuditCacheFile(file)
	if err != nil {
		return errors.Join(err, file.Close())
	}
	defer func() {
		if unlockErr := unlock(); err == nil && unlockErr != nil {
			err = unlockErr
		}
		if closeErr := file.Close(); err == nil && closeErr != nil {
			err = closeErr
		}
	}()
	return fn()
}

func ensureAuditCacheDirectory(root string) error {
	root = filepath.Clean(root)
	info, err := os.Stat(root)
	if err != nil {
		return fmt.Errorf("inspect audit root: %w", err)
	}
	if !info.IsDir() {
		return fmt.Errorf("audit root is not a directory: %s", root)
	}
	current := root
	for _, component := range []string{".reconc", "cache"} {
		current = filepath.Join(current, component)
		entry, inspectErr := os.Lstat(current)
		if errors.Is(inspectErr, os.ErrNotExist) {
			if mkdirErr := os.Mkdir(current, 0o755); mkdirErr != nil && !errors.Is(mkdirErr, os.ErrExist) {
				return fmt.Errorf("create audit cache directory %s: %w", current, mkdirErr)
			}
			entry, inspectErr = os.Lstat(current)
		}
		if inspectErr != nil {
			return fmt.Errorf("inspect audit cache directory %s: %w", current, inspectErr)
		}
		if entry.Mode()&os.ModeSymlink != 0 || !entry.IsDir() {
			return fmt.Errorf("audit cache directory must be a non-symlink directory: %s", current)
		}
	}
	return nil
}
