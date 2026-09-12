# TASK 530: Detect dirty TASK ancestors and Gitlinks

## Why

Completion with `require_committed` identifies dirty TASK control-plane files by
matching the exact overview, the detail directory, descendants, and ancestor
directories. The ancestor branch currently runs only when the Git-reported path
ends with `/`. Git normally reports a changed submodule or Gitlink as a bare
path such as `docs`, so a dirty Gitlink containing `docs/tasks.md` or
`docs/tasks/**` can escape the completion and terminal Stop gate.

This task implements audit candidate C242. Matching must use path segments and
configured ownership rather than a presentation-specific trailing slash, while
continuing to exclude similarly prefixed unrelated files and directories.

## Acceptance

- Normalize configured TASK paths and Git-reported dirty paths into canonical
  repository-relative slash-separated segment sequences before comparison.
- Match the exact overview path, the exact detail directory, every descendant
  of the detail directory, and every proper ancestor that contains either
  configured TASK path, regardless of a trailing slash.
- Recognize bare dirty Gitlink/submodule ancestors such as `docs` when the
  overview is `docs/tasks.md` or the detail root is `docs/tasks`.
- Preserve exact segment boundaries: `doc`, `docs-old`, `documentation`,
  `tasks.md.bak`, and `docs/taskset` must not match `docs` or `docs/tasks` by
  string-prefix accident.
- Handle overview and detail paths with different ancestors, root-level files,
  nested custom layouts, dots within segment names, and normalized redundant
  separators according to the existing config validation contract.
- Preserve input order and stable uniqueness of returned dirty paths; do not
  rewrite Git's displayed path in diagnostics.
- Keep completion and terminal Stop on the same `DirtyCompletionPaths` source
  of truth. Do not add adapter-specific Gitlink exceptions.
- Preserve read-only inspection and existing Git bounds, status parsing,
  symlink/path-identity checks, commit-candidate binding, and task-layout
  validation.
- Add table-driven unit cases for exact paths, descendants, ancestors with and
  without `/`, Gitlinks, prefix collisions, root-level configurations, and
  multiple independent TASK roots.
- Add a real Git integration fixture containing a submodule or Gitlink-backed
  TASK ancestor and prove `require_committed` blocks both final completion and
  terminal Stop while it is dirty.
- Prove unrelated dirty submodules remain outside the TASK completion gate.
- Update completion documentation to state that dirty ancestor Gitlinks are
  owned by the TASK control plane.
- Pass tasklifecycle, Git integration, completion, Stop, and race tests plus all
  required repository gates.

## Sub-Tasks

- [x] Read TASK config validation, path ownership, DirtyCompletionPaths, all callers, Git porcelain parsing, completion candidate binding, terminal Stop, and existing integration fixtures.
- [x] Define one segment-aware ancestor/descendant relation over already validated repository-relative paths, preserving original dirty-path output.
- [x] Replace the trailing-slash-dependent condition in the canonical matcher without broadening unrelated prefix matches.
- [x] Add exhaustive table tests and a real dirty-Gitlink integration regression for completion and Stop parity.
- [x] Verify custom overview/detail layouts, root paths, symlinks, renamed paths, and unrelated submodules retain correct behavior.
- [x] Confirm matcher work stays linear in bounded dirty-path and configured-path size and introduces no filesystem walk.
- [x] Update completion/TASK path-ownership documentation.
- [x] Run focused race and Git integration tests plus all required repository gates; re-read every modified file before archival.

## Notes

### Observed behavior

- `DirtyCompletionPaths` previously gated proper-ancestor matching on a
  trailing `/`. Git reports a dirty Gitlink as a bare path such as `docs`.

### Implementation

- Matching canonicalizes configured and Git-reported paths to slash-separated
  segments (`path.Clean`, backslash conversion) without rewriting diagnostics.
- Owned: exact overview, the detail directory and its descendants, and proper
  ancestors of either configured path. Prefix lookalikes stay outside.
- Completion `Evaluate` and terminal Stop share the same matcher. A real
  submodule fixture proves a dirty `docs` Gitlink blocks both gates while an
  unrelated dirty `vendor/lib` Gitlink does not.

### Verification

- Table tests for default, split, nested, root, dotted, collision, uniqueness,
  and escape cases. Completion and Stop Gitlink integration tests plus
  `go test -race` on tasklifecycle, completiongate, and agentsession,
  `make lint`, and `make test` passed.

### Matching invariants

- Relations are defined over complete path segments.
- Exact overview and detail paths are always owned.
- Descendants of the detail directory are owned.
- Proper ancestors of either configured path are owned because their dirty
  Gitlink/directory identity can contain the control plane.
- Siblings and lexical prefix lookalikes are not owned.
- Returned values preserve the original Git evidence string and order.

### Verification matrix

- Default `docs/tasks.md` and `docs/tasks` layout.
- Bare `docs`, `docs/`, exact overview, exact detail root, nested detail, and
  archived detail.
- `doc`, `docs2`, `docs-old`, `documentation`, `docs/task`, and backup suffixes.
- Custom roots sharing no ancestor and roots sharing several ancestors.
- Real clean and dirty Gitlinks, unrelated submodule, staged parent change, and
  worktree-only submodule dirtiness.

## Deviations

None.
