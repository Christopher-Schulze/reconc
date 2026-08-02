package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// The cached* wrappers below define the canonical input fingerprint for each
// deterministic-input sub-audit. Audits whose result depends on a broad
// directory walk (arch-boundaries, module-contracts, test-coverage,
// repo-layout) or on a subprocess (generated-references) and the lightweight
// agent-hooks audit are intentionally not cached because the input set is
// either too broad to fingerprint cheaply or too small to benefit.

func cachedTaskState(root string) []string {
	archiveRevision, cacheable := taskArchiveRevision(root)
	if !cacheable {
		return auditTaskState(root)
	}
	inputs := taskStateCacheInputs(root)
	inputs.AddValue("task-archive-tree", archiveRevision)
	return runWithCache(root, "task-state", inputs, func() []string {
		return auditTaskState(root)
	})
}

func taskStateCacheInputs(root string) *cacheInputs {
	inputs := newCacheInputs()
	tasksPath := filepath.Join(root, "docs/tasks.md")
	inputs.AddFile(tasksPath)
	if body, err := os.ReadFile(tasksPath); err == nil {
		index, _ := parseTaskIndex(string(body))
		for _, entry := range index.entries {
			if entry.icon == "x" {
				continue
			}
			inputs.AddFile(filepath.Join(root, "docs", filepath.FromSlash(entry.target)))
		}
	}
	inputs.AddPathMetadata(filepath.Join(root, "docs/tasks"))
	inputs.AddPathMetadata(filepath.Join(root, "docs/tasks/done"))
	inputs.AddFile(filepath.Join(root, "docs/spec.md"))
	inputs.AddFile(filepath.Join(root, filepath.FromSlash(schemaRel)))
	return inputs
}

// taskArchiveRevision returns the committed archive tree only when the
// archive worktree is clean. Dirty, untracked, or unreadable archive state
// bypasses the cache entirely, so archived TASK edits can never reuse a
// stale pass while clean hot-path checks avoid reading every archive file.
func taskArchiveRevision(root string) (string, bool) {
	return taskArchiveRevisionWithTimeout(root, shortAuditCommandTimeout)
}

func taskArchiveRevisionWithTimeout(root string, timeout time.Duration) (string, bool) {
	status, err := runAuditCommand(timeout, "git", "-C", root, "status", "--porcelain=v1", "-z", "--untracked-files=normal", "--", "docs/tasks/done")
	if err != nil || len(status) != 0 {
		return "", false
	}
	revision, err := runAuditCommand(timeout, "git", "-C", root, "rev-parse", "--verify", "HEAD:docs/tasks/done")
	if err != nil {
		entries, readErr := os.ReadDir(filepath.Join(root, "docs/tasks/done"))
		if errors.Is(readErr, os.ErrNotExist) || (readErr == nil && len(entries) == 0) {
			return "absent", true
		}
		return "", false
	}
	return strings.TrimSpace(string(revision)), true
}

func cachedSpecFormat(root string) []string {
	inputs := newCacheInputs()
	inputs.AddFile(filepath.Join(root, "docs/spec.md"))
	return runWithCache(root, "spec-format", inputs, func() []string {
		return auditSpecFormat(root)
	})
}

func cachedSchemaPresent(root string) []string {
	inputs := newCacheInputs()
	inputs.AddFile(filepath.Join(root, filepath.FromSlash(schemaRel)))
	inputs.AddFile(filepath.Join(root, "tools/reconc/harness/template/audits/lib/donecheck/schema.go"))
	return runWithCache(root, "schema-present", inputs, func() []string {
		return auditSchemaPresent(root)
	})
}

func cachedAgentsMdMirror(root string) []string {
	inputs := newCacheInputs()
	inputs.AddFile(filepath.Join(root, "AGENTS.md"))
	inputs.AddFile(filepath.Join(root, filepath.FromSlash(schemaRel)))
	return runWithCache(root, "agents-md-mirror", inputs, func() []string {
		return auditAgentsMdMirror(root)
	})
}

func cachedStartEntrypoint(root string) []string {
	inputs := newCacheInputs()
	inputs.AddFile(filepath.Join(root, "start.md"))
	inputs.AddFile(filepath.Join(root, "AGENTS.md"))
	return runWithCache(root, "start-entrypoint", inputs, func() []string {
		return auditStartEntrypoint(root)
	})
}

func cachedBuildBaseline(root string) []string {
	inputs := newCacheInputs()
	inputs.AddFile(stackConfigPath(root))
	for _, rel := range []string{
		"go.mod",
		"Cargo.toml",
		projectRel(root, "frontend/package.json"),
		projectRel(root, "scripts/build/build.go"),
		projectRel(root, "scripts/build/build_test.go"),
		projectRel(root, "backend/project/main.go"),
	} {
		inputs.AddFile(filepath.Join(root, filepath.FromSlash(rel)))
	}
	return runWithCache(root, "build-baseline", inputs, func() []string {
		return auditBuildBaseline(root)
	})
}

// cachedTestCoverage caches the test-coverage audit using a STRUCTURE-only
// fingerprint: only file paths and existence matter, not content. This means
// editing the body of an existing Go file does not invalidate the cache,
// which matches the audit's actual semantics ("does each Go directory
// contain at least one *_test.go?"). Adding/removing files or directories
// re-runs the audit.
func cachedTestCoverage(root string) []string {
	inputs := newCacheInputs()
	inputs.AddTreeStructure(projectRoot(root), []string{".go"})
	inputs.AddTreeStructure(filepath.Join(root, "tools/reconc"), []string{".go"})
	return runWithCache(root, "test-coverage", inputs, func() []string {
		return auditTestCoverage(root)
	})
}

func cachedDurableStoreBaseline(root string) []string {
	inputs := newCacheInputs()
	inputs.AddFile(stackConfigPath(root))
	for _, rel := range []string{
		projectRel(root, "backend/project/internal/store/store.go"),
		projectRel(root, "backend/project/internal/store/hash.go"),
		projectRel(root, "backend/project/internal/store/store_test.go"),
		projectRel(root, "db/migrations/migrations.go"),
		projectRel(root, "db/migrations/migrations_test.go"),
		projectRel(root, "db/migrations/project/core/001_initial.sql"),
	} {
		inputs.AddFile(filepath.Join(root, filepath.FromSlash(rel)))
	}
	return runWithCache(root, "durable-store", inputs, func() []string {
		return auditDurableStoreBaseline(root)
	})
}
