package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
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
const cacheVersion = "v7-2026-07-19"

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
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
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
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		c.inputErrors = append(c.inputErrors, fmt.Errorf("walk tree structure %s: %w", root, err))
	}
}

// AddTree appends every regular file under root with one of the suffixes.
// Returns silently if root does not exist.
func (c *cacheInputs) AddTree(root string, suffixes []string) {
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
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
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		c.inputErrors = append(c.inputErrors, fmt.Errorf("walk tree %s: %w", root, err))
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
		content, err := os.ReadFile(path)
		if err != nil {
			if !errors.Is(err, os.ErrNotExist) {
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
			digest.Write([]byte("ABSENT"))
		} else {
			fmt.Fprintf(digest, "%s\x00%d\x00%d", info.Mode().String(), info.Size(), info.ModTime().UnixNano())
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
		result := fn()
		return append(result, fmt.Sprintf("audit %s cache input failed: %v", name, err))
	}
	keyLock := auditCacheKeyLock(root, name)
	keyLock.Lock()
	defer keyLock.Unlock()

	cachePath := filepath.Join(root, filepath.FromSlash(cacheRel))
	var result []string
	err = withAuditCacheNamedLock(auditCacheKeyLockPath(root, name), func() error {
		if auditCacheHasPass(cachePath, name, hash) {
			return nil
		}
		result = fn()
		publishAuditCacheResult(cachePath, name, hash, result)
		return nil
	})
	if err != nil {
		return fn()
	}
	return result
}

func auditCacheHasPass(cachePath, name, hash string) bool {
	auditCacheMu.Lock()
	defer auditCacheMu.Unlock()
	hit := false
	err := withAuditCacheNamedLock(cachePath+".lock", func() error {
		cache := loadCacheFile(cachePath)
		entry, ok := cache.Entries[name]
		hit = ok && entry.Version == cacheVersion && entry.Result == cachePassTag && entry.Hash == hash
		return nil
	})
	return err == nil && hit
}

func publishAuditCacheResult(cachePath, name, hash string, result []string) {
	auditCacheMu.Lock()
	defer auditCacheMu.Unlock()
	_ = withAuditCacheNamedLock(cachePath+".lock", func() error {
		cache := loadCacheFile(cachePath)
		if len(result) == 0 {
			cache.Entries[name] = cacheEntry{Hash: hash, Result: cachePassTag, Version: cacheVersion}
		} else {
			delete(cache.Entries, name)
		}
		_ = saveCacheFile(cachePath, cache)
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
	bytes, err := os.ReadFile(path)
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

func saveCacheFile(path string, cache cacheFile) bool {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
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
	return os.Rename(tmp.Name(), path) == nil
}

func withAuditCacheNamedLock(lockPath string, fn func() error) (err error) {
	if err := os.MkdirAll(filepath.Dir(lockPath), 0o755); err != nil {
		return err
	}
	file, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return err
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
