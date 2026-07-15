package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

const auditCacheProcessHelperEnv = "RECONC_AUDIT_CACHE_PROCESS_HELPER"

func writeCacheFixture(t testing.TB, root string, rel string, content string) {
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

func TestRunWithCacheIndependentColdKeysRunConcurrentlyAndMerge(t *testing.T) {
	root := t.TempDir()
	writeCacheFixture(t, root, "input.txt", "hello")
	started := make(chan string, 2)
	release := make(chan struct{})
	var workers sync.WaitGroup
	for _, name := range []string{"audit-a", "audit-b"} {
		name := name
		workers.Add(1)
		go func() {
			defer workers.Done()
			inputs := newCacheInputs()
			inputs.AddFile(filepath.Join(root, "input.txt"))
			runWithCache(root, name, inputs, func() []string {
				started <- name
				<-release
				return nil
			})
		}()
	}

	for count := 0; count < 2; count++ {
		select {
		case <-started:
		case <-time.After(2 * time.Second):
			close(release)
			workers.Wait()
			t.Fatal("independent cold audit keys were serialized")
		}
	}
	close(release)
	workers.Wait()

	cachePath := filepath.Join(root, filepath.FromSlash(cacheRel))
	body, err := os.ReadFile(cachePath)
	if err != nil {
		t.Fatal(err)
	}
	var cache cacheFile
	if err := json.Unmarshal(body, &cache); err != nil {
		t.Fatalf("concurrent publication corrupted cache JSON: %v", err)
	}
	if len(cache.Entries) != 2 {
		t.Fatalf("concurrent publication lost an entry: %#v", cache.Entries)
	}
}

func TestRunWithCacheSameColdKeySingleflights(t *testing.T) {
	root := t.TempDir()
	writeCacheFixture(t, root, "input.txt", "hello")
	start := make(chan struct{})
	firstEntered := make(chan struct{}, 1)
	release := make(chan struct{})
	var calls atomic.Int32
	var workers sync.WaitGroup
	for worker := 0; worker < 2; worker++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			<-start
			inputs := newCacheInputs()
			inputs.AddFile(filepath.Join(root, "input.txt"))
			runWithCache(root, "same-audit", inputs, func() []string {
				calls.Add(1)
				firstEntered <- struct{}{}
				<-release
				return nil
			})
		}()
	}
	close(start)
	select {
	case <-firstEntered:
	case <-time.After(2 * time.Second):
		close(release)
		workers.Wait()
		t.Fatal("cold audit never started")
	}
	select {
	case <-firstEntered:
		close(release)
		workers.Wait()
		t.Fatal("same cold audit key executed twice")
	case <-time.After(250 * time.Millisecond):
	}
	close(release)
	workers.Wait()
	if got := calls.Load(); got != 1 {
		t.Fatalf("same cold audit key must execute once, got %d", got)
	}
}

func TestRunWithCacheCrossProcessColdKeysMerge(t *testing.T) {
	root := t.TempDir()
	writeCacheFixture(t, root, "input.txt", "hello")
	releasePath := filepath.Join(root, "release")
	type child struct {
		command *exec.Cmd
		output  bytes.Buffer
	}
	children := make([]child, 2)
	for index, name := range []string{"process-a", "process-b"} {
		readyPath := filepath.Join(root, name+".ready")
		command := exec.Command(os.Args[0], "-test.run=^TestAuditCacheCrossProcessHelper$", "-test.count=1")
		command.Env = append(os.Environ(),
			auditCacheProcessHelperEnv+"=1",
			"RECONC_AUDIT_CACHE_ROOT="+root,
			"RECONC_AUDIT_CACHE_NAME="+name,
			"RECONC_AUDIT_CACHE_READY="+readyPath,
			"RECONC_AUDIT_CACHE_RELEASE="+releasePath,
		)
		command.Stdout = &children[index].output
		command.Stderr = &children[index].output
		children[index].command = command
		if err := command.Start(); err != nil {
			t.Fatal(err)
		}
	}

	deadline := time.Now().Add(3 * time.Second)
	for {
		ready := true
		for _, name := range []string{"process-a", "process-b"} {
			if _, err := os.Stat(filepath.Join(root, name+".ready")); err != nil {
				ready = false
			}
		}
		if ready || time.Now().After(deadline) {
			if !ready {
				writeCacheFixture(t, root, "release", "release")
				for index := range children {
					_ = children[index].command.Wait()
				}
				t.Fatal("cross-process cold audits did not overlap")
			}
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	writeCacheFixture(t, root, "release", "release")
	for index := range children {
		if err := children[index].command.Wait(); err != nil {
			t.Fatalf("cache child %d: %v\n%s", index, err, children[index].output.String())
		}
	}
	cache := loadCacheFile(filepath.Join(root, filepath.FromSlash(cacheRel)))
	if len(cache.Entries) != 2 {
		t.Fatalf("cross-process publication lost an entry: %#v", cache.Entries)
	}
}

func TestAuditCacheCrossProcessHelper(t *testing.T) {
	if os.Getenv(auditCacheProcessHelperEnv) == "" {
		t.Skip("process helper")
	}
	root := os.Getenv("RECONC_AUDIT_CACHE_ROOT")
	name := os.Getenv("RECONC_AUDIT_CACHE_NAME")
	inputs := newCacheInputs()
	inputs.AddFile(filepath.Join(root, "input.txt"))
	result := runWithCache(root, name, inputs, func() []string {
		if err := os.WriteFile(os.Getenv("RECONC_AUDIT_CACHE_READY"), []byte("ready"), 0o644); err != nil {
			t.Fatal(err)
		}
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			if _, err := os.Stat(os.Getenv("RECONC_AUDIT_CACHE_RELEASE")); err == nil {
				return nil
			}
			time.Sleep(5 * time.Millisecond)
		}
		t.Fatal("release timeout")
		return nil
	})
	if len(result) != 0 {
		t.Fatalf("helper audit failed: %v", result)
	}
}

func TestRunWithCacheCrossProcessSameKeySingleflights(t *testing.T) {
	root := t.TempDir()
	writeCacheFixture(t, root, "input.txt", "hello")
	releasePath := filepath.Join(root, "release")
	readyPaths := []string{filepath.Join(root, "first.ready"), filepath.Join(root, "second.ready")}
	commands := make([]*exec.Cmd, 2)
	outputs := make([]bytes.Buffer, 2)
	startChild := func(index int) {
		t.Helper()
		command := exec.Command(os.Args[0], "-test.run=^TestAuditCacheCrossProcessHelper$", "-test.count=1")
		command.Env = append(os.Environ(),
			auditCacheProcessHelperEnv+"=1",
			"RECONC_AUDIT_CACHE_ROOT="+root,
			"RECONC_AUDIT_CACHE_NAME=same-process-key",
			"RECONC_AUDIT_CACHE_READY="+readyPaths[index],
			"RECONC_AUDIT_CACHE_RELEASE="+releasePath,
		)
		command.Stdout = &outputs[index]
		command.Stderr = &outputs[index]
		commands[index] = command
		if err := command.Start(); err != nil {
			t.Fatal(err)
		}
	}
	startChild(0)
	deadline := time.Now().Add(3 * time.Second)
	for {
		if _, err := os.Stat(readyPaths[0]); err == nil {
			break
		}
		if time.Now().After(deadline) {
			writeCacheFixture(t, root, "release", "release")
			_ = commands[0].Wait()
			t.Fatalf("first same-key cache child did not enter audit\n%s", outputs[0].String())
		}
		time.Sleep(5 * time.Millisecond)
	}
	startChild(1)
	time.Sleep(250 * time.Millisecond)
	if _, err := os.Stat(readyPaths[1]); err == nil {
		writeCacheFixture(t, root, "release", "release")
		for _, command := range commands {
			_ = command.Wait()
		}
		t.Fatal("same cache key executed concurrently in separate processes")
	}
	writeCacheFixture(t, root, "release", "release")
	for index, command := range commands {
		if err := command.Wait(); err != nil {
			t.Fatalf("same-key cache child %d: %v\n%s", index, err, outputs[index].String())
		}
	}
	if _, err := os.Stat(readyPaths[1]); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("second same-key audit function ran unexpectedly: %v", err)
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

func TestRunWithCacheFailsClosedOnTreeWalkError(t *testing.T) {
	tests := []struct {
		name string
		add  func(*cacheInputs, string)
	}{
		{name: "content", add: func(inputs *cacheInputs, root string) { inputs.AddTree(root, nil) }},
		{name: "structure", add: func(inputs *cacheInputs, root string) { inputs.AddTreeStructure(root, nil) }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			inputs := newCacheInputs()
			test.add(inputs, filepath.Join(root, "invalid\x00tree"))
			calls := 0
			result := runWithCache(root, "walk-error-"+test.name, inputs, func() []string {
				calls++
				return nil
			})
			if calls != 1 || !containsFailure(result, "cache input failed") {
				t.Fatalf("walk failure must execute the audit and fail closed: calls=%d result=%v", calls, result)
			}
			if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(cacheRel))); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("walk failure must not publish a cache pass: %v", err)
			}
		})
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

func TestCacheInputsPathMetadataDetectsDirectoryEntryChange(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "archive")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(dir, time.Unix(1_000, 0), time.Unix(1_000, 0)); err != nil {
		t.Fatal(err)
	}
	first := newCacheInputs()
	first.AddPathMetadata(dir)
	hashBefore, err := first.Hash()
	if err != nil {
		t.Fatal(err)
	}
	writeCacheFixture(t, root, "archive/TASK-0001-Done.md", "done")
	if err := os.Chtimes(dir, time.Unix(2_000, 0), time.Unix(2_000, 0)); err != nil {
		t.Fatal(err)
	}
	second := newCacheInputs()
	second.AddPathMetadata(dir)
	hashAfter, err := second.Hash()
	if err != nil {
		t.Fatal(err)
	}
	if hashBefore == hashAfter {
		t.Fatal("directory entry changes must invalidate path metadata")
	}
}

func TestTaskStateCacheInputsHashOnlyOpenTaskBodies(t *testing.T) {
	root := t.TempDir()
	writeCacheFixture(t, root, "docs/tasks.md", "# Tasks\n\nCurrent: TASK-0002-Open -> tasks/TASK-0002-Open.md\n\n- [x] TASK-0001-Done - done -> tasks/done/TASK-0001-Done.md\n- [ ] TASK-0002-Open - open -> tasks/TASK-0002-Open.md\n")
	writeCacheFixture(t, root, "docs/tasks/TASK-0002-Open.md", "open-v1")
	writeCacheFixture(t, root, "docs/tasks/done/TASK-0001-Done.md", "done-v1")
	inputs := taskStateCacheInputs(root)
	donePath := filepath.Join(root, "docs/tasks/done/TASK-0001-Done.md")
	for _, path := range inputs.files {
		if path == donePath {
			t.Fatal("archived TASK body leaked into the hot-path content hash")
		}
	}
	firstHash, err := inputs.Hash()
	if err != nil {
		t.Fatal(err)
	}
	writeCacheFixture(t, root, "docs/tasks/done/TASK-0001-Done.md", "done-v2")
	secondHash, err := taskStateCacheInputs(root).Hash()
	if err != nil {
		t.Fatal(err)
	}
	if firstHash != secondHash {
		t.Fatal("archived TASK body edits must not trigger full archive hashing")
	}
	writeCacheFixture(t, root, "docs/tasks/TASK-0002-Open.md", "open-v2")
	thirdHash, err := taskStateCacheInputs(root).Hash()
	if err != nil {
		t.Fatal(err)
	}
	if secondHash == thirdHash {
		t.Fatal("open TASK body edits must invalidate task-state cache")
	}
}

func TestTaskArchiveRevisionBypassesDirtyArchiveAndTracksCommit(t *testing.T) {
	root := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		if out, err := exec.Command("git", append([]string{"-C", root}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	git("init", "--quiet")
	git("config", "user.email", "test@test")
	git("config", "user.name", "test")
	writeCacheFixture(t, root, "docs/tasks/done/TASK-0001-Done.md", "done-v1")
	git("add", "docs/tasks/done/TASK-0001-Done.md")
	git("commit", "-m", "initial archive", "--quiet")
	firstRevision, cacheable := taskArchiveRevision(root)
	if !cacheable || firstRevision == "" || firstRevision == "absent" {
		t.Fatalf("clean committed archive must be cacheable, got revision=%q cacheable=%t", firstRevision, cacheable)
	}
	writeCacheFixture(t, root, "docs/tasks/done/TASK-0001-Done.md", "done-v2")
	if _, cacheable := taskArchiveRevision(root); cacheable {
		t.Fatal("dirty archived TASK must bypass cache")
	}
	git("add", "docs/tasks/done/TASK-0001-Done.md")
	git("commit", "-m", "update archive", "--quiet")
	secondRevision, cacheable := taskArchiveRevision(root)
	if !cacheable || secondRevision == firstRevision {
		t.Fatalf("archive tree revision must change after commit: first=%q second=%q cacheable=%t", firstRevision, secondRevision, cacheable)
	}
}

func BenchmarkRunWithCacheIndependentColdKeys(b *testing.B) {
	root := b.TempDir()
	writeCacheFixture(b, root, "input.txt", "hello")
	b.ReportAllocs()
	b.ResetTimer()
	for iteration := 0; iteration < b.N; iteration++ {
		var workers sync.WaitGroup
		for _, name := range []string{"audit-a", "audit-b"} {
			name := name
			workers.Add(1)
			go func() {
				defer workers.Done()
				inputs := newCacheInputs()
				inputs.AddFile(filepath.Join(root, "input.txt"))
				inputs.AddValue("iteration", strconv.Itoa(iteration))
				runWithCache(root, name, inputs, func() []string { return nil })
			}()
		}
		workers.Wait()
	}
}
