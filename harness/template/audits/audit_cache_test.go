package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeCacheFixture(t *testing.T, root string, rel string, content string) {
	t.Helper()
	full := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
}

func TestRunWithCacheSkipsOnRepeatPass(t *testing.T) {
	root := t.TempDir()
	writeCacheFixture(t, root, "input.txt", "hello")
	calls := 0
	inputsBuilder := func() *cacheInputs {
		c := newCacheInputs()
		c.AddFile(filepath.Join(root, "input.txt"))
		return c
	}
	first := runWithCache(root, "test-audit", inputsBuilder(), func() []string {
		calls++
		return nil
	})
	if first != nil {
		t.Fatalf("first run must pass, got %v", first)
	}
	if calls != 1 {
		t.Fatalf("first run must call fn once, got %d", calls)
	}
	second := runWithCache(root, "test-audit", inputsBuilder(), func() []string {
		calls++
		return []string{"should not be called"}
	})
	if second != nil {
		t.Fatalf("cached pass must return nil, got %v", second)
	}
	if calls != 1 {
		t.Fatalf("cached pass must skip fn, calls=%d", calls)
	}
}

func TestRunWithCacheReRunsOnInputChange(t *testing.T) {
	root := t.TempDir()
	writeCacheFixture(t, root, "input.txt", "hello")
	inputsBuilder := func() *cacheInputs {
		c := newCacheInputs()
		c.AddFile(filepath.Join(root, "input.txt"))
		return c
	}
	if got := runWithCache(root, "audit", inputsBuilder(), func() []string { return nil }); got != nil {
		t.Fatalf("first pass: %v", got)
	}
	writeCacheFixture(t, root, "input.txt", "changed")
	calls := 0
	if got := runWithCache(root, "audit", inputsBuilder(), func() []string {
		calls++
		return nil
	}); got != nil {
		t.Fatalf("changed input: %v", got)
	}
	if calls != 1 {
		t.Fatalf("changed input must trigger re-run, calls=%d", calls)
	}
}

func TestRunWithCacheDoesNotPersistFailure(t *testing.T) {
	root := t.TempDir()
	writeCacheFixture(t, root, "input.txt", "hello")
	inputsBuilder := func() *cacheInputs {
		c := newCacheInputs()
		c.AddFile(filepath.Join(root, "input.txt"))
		return c
	}
	first := runWithCache(root, "audit", inputsBuilder(), func() []string {
		return []string{"fail"}
	})
	if len(first) != 1 {
		t.Fatalf("first must report failure, got %v", first)
	}
	calls := 0
	second := runWithCache(root, "audit", inputsBuilder(), func() []string {
		calls++
		return []string{"fail again"}
	})
	if len(second) != 1 {
		t.Fatalf("second must re-run and report, got %v", second)
	}
	if calls != 1 {
		t.Fatalf("failure must not be cached, calls=%d", calls)
	}
}

func TestRunWithCacheBypassedByEnv(t *testing.T) {
	root := t.TempDir()
	writeCacheFixture(t, root, "input.txt", "hello")
	inputsBuilder := func() *cacheInputs {
		c := newCacheInputs()
		c.AddFile(filepath.Join(root, "input.txt"))
		return c
	}
	if got := runWithCache(root, "audit", inputsBuilder(), func() []string { return nil }); got != nil {
		t.Fatalf("first: %v", got)
	}
	t.Setenv(cacheEnv, "1")
	calls := 0
	runWithCache(root, "audit", inputsBuilder(), func() []string {
		calls++
		return nil
	})
	if calls != 1 {
		t.Fatalf("env bypass must force re-run, calls=%d", calls)
	}
}

func TestRunWithCacheVersionInvalidates(t *testing.T) {
	root := t.TempDir()
	writeCacheFixture(t, root, "input.txt", "hello")
	cachePath := filepath.Join(root, filepath.FromSlash(cacheRel))
	if err := os.MkdirAll(filepath.Dir(cachePath), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	stale := cacheFile{Entries: map[string]cacheEntry{
		"audit": {Hash: "anything", Result: cachePassTag, Version: "old-version"},
	}}
	bytes, _ := json.Marshal(stale)
	if err := os.WriteFile(cachePath, bytes, 0o644); err != nil {
		t.Fatalf("write cache: %v", err)
	}
	calls := 0
	runWithCache(root, "audit", func() *cacheInputs {
		c := newCacheInputs()
		c.AddFile(filepath.Join(root, "input.txt"))
		return c
	}(), func() []string {
		calls++
		return nil
	})
	if calls != 1 {
		t.Fatalf("old-version cache must re-run, calls=%d", calls)
	}
}

func TestCacheInputsTreeRecursive(t *testing.T) {
	root := t.TempDir()
	writeCacheFixture(t, root, "tree/a.go", "package x")
	writeCacheFixture(t, root, "tree/sub/b.go", "package y")
	writeCacheFixture(t, root, "tree/c.txt", "ignore me")
	c := newCacheInputs()
	c.AddTree(filepath.Join(root, "tree"), []string{".go"})
	if len(c.files) != 2 {
		t.Fatalf("expected 2 .go files, got %d (%v)", len(c.files), c.files)
	}
	for _, f := range c.files {
		if !strings.HasSuffix(f, ".go") {
			t.Fatalf("non-Go file leaked: %s", f)
		}
	}
}

func TestCacheInputsHashStable(t *testing.T) {
	root := t.TempDir()
	writeCacheFixture(t, root, "a.txt", "alpha")
	writeCacheFixture(t, root, "b.txt", "beta")
	c1 := newCacheInputs()
	c1.AddFile(filepath.Join(root, "a.txt"))
	c1.AddFile(filepath.Join(root, "b.txt"))
	c2 := newCacheInputs()
	c2.AddFile(filepath.Join(root, "b.txt"))
	c2.AddFile(filepath.Join(root, "a.txt"))
	h1, err := c1.Hash()
	if err != nil {
		t.Fatalf("hash1: %v", err)
	}
	h2, err := c2.Hash()
	if err != nil {
		t.Fatalf("hash2: %v", err)
	}
	if h1 != h2 {
		t.Fatalf("hash must be order-independent, %q != %q", h1, h2)
	}
}

func TestCacheInputsAbsentVsPresent(t *testing.T) {
	root := t.TempDir()
	c := newCacheInputs()
	c.AddFile(filepath.Join(root, "missing.txt"))
	h1, _ := c.Hash()
	writeCacheFixture(t, root, "missing.txt", "now exists")
	c2 := newCacheInputs()
	c2.AddFile(filepath.Join(root, "missing.txt"))
	h2, _ := c2.Hash()
	if h1 == h2 {
		t.Fatal("hash must differ for absent vs present file")
	}
}
