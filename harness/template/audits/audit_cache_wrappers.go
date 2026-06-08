package main

import (
	"path/filepath"
)

// The cached* wrappers below define the canonical input fingerprint for each
// deterministic-input sub-audit. Audits whose result depends on a broad
// directory walk (arch-boundaries, module-contracts, test-coverage,
// repo-layout) or on a subprocess (generated-references) and the lightweight
// agent-hooks audit are intentionally not cached because the input set is
// either too broad to fingerprint cheaply or too small to benefit.

func cachedTaskState(root string) []string {
	inputs := newCacheInputs()
	inputs.AddFile(filepath.Join(root, "docs/tasks.md"))
	inputs.AddTree(filepath.Join(root, "docs/tasks"), []string{".md"})
	inputs.AddFile(filepath.Join(root, filepath.FromSlash(schemaRel)))
	return runWithCache(root, "task-state", inputs, func() []string {
		return auditTaskState(root)
	})
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
