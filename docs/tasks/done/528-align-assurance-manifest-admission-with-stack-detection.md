# TASK 528: Align assurance manifest admission with stack detection

## Why

Assurance module scoping validates every changed path whose basename resembles a
Go, Rust, or Python manifest and matches the configured manifest patterns.
Stack detection separately refuses to enter dependency/build directories and
stops beyond its bounded discovery depth. Because changed-manifest validation
does not reuse that path-admission contract, a changed or deleted manifest under
`vendor`, `node_modules`, `target`, another ignored tree, or excessive depth can
block assurance even though it could never define a detected module.

This task implements the narrow valid portion of audit candidate C230. It must
remove impossible module roots without weakening the deliberate fail-closed
error for a deleted or non-regular manifest at a path that stack detection would
otherwise admit.

## Acceptance

- Expose or reuse one path-only stack-detection admission function that applies
  the canonical separator, case, ignored-directory, maximum-depth, and file-kind
  rules required to decide whether a manifest path can define a module.
- Use the same admission result before assurance validates a changed manifest;
  do not duplicate the ignored-directory list or depth constant in assurance.
- Ignore changed manifests beneath `.git`, `.reconc`, dependency, generated,
  coverage, output, build, target, vendor, virtual-environment, and other
  directories already excluded by stack detection.
- Apply the exact discovery depth boundary consistently. A manifest at the
  deepest admissible level remains eligible; the next deeper level is ignored.
- Preserve case-insensitive manifest-name recognition where currently
  supported and normalize Git slash paths without accepting absolute,
  parent-escaping, empty, dot, or non-canonical paths.
- Continue rejecting a deleted, unreadable, non-regular, symlinked, malformed,
  or replaced manifest at an otherwise admissible module path according to the
  existing path-identity and assurance fail-closed contract.
- Preserve workspace ownership, deepest-module selection, module command
  working directories, changed-path assignment, applicable patterns, and
  missing-module-evidence behavior.
- Ensure an ignored manifest cannot suppress or impersonate an admissible root
  or nested module with the same stack.
- Add table-driven boundary tests for every ignored directory, mixed case,
  Windows-style input separators, depth limits, root and nested manifests,
  deleted admissible manifests, deleted ignored manifests, symlinks, and
  overlapping workspaces.
- Keep detection and assurance read-only and bounded; do not add a second tree
  walk solely to classify changed paths.
- Update assurance/module-discovery documentation if its stated path scope is
  incomplete.
- Pass stack-detection and assurance tests under the race detector plus every
  required repository gate.

## Sub-Tasks

- [x] Read all stack-detection traversal/admission helpers, module candidate construction, assurance scope creation, changed-manifest validation, workspace ownership, and caller path contracts.
- [x] Define a reusable path-only manifest-admission API after reading its exact callers and return requirements; keep stack-detection constants single-source.
- [x] Apply admission before changed-manifest verification while preserving fail-closed handling for every admissible missing or invalid manifest.
- [x] Add ignored-tree, depth-boundary, separator, case, deletion, replacement, symlink, workspace, and overlap regressions.
- [x] Verify no package-script, dependency-pin, source-hygiene, or non-module assurance gate changes behavior unintentionally.
- [x] Measure traversal and changed-path classification to confirm no new walk or unbounded work was introduced.
- [x] Propagate the final discovery/assurance scope contract into documentation where user-visible.
- [x] Run focused race tests and all required repository gates; re-read every modified file before archival.

## Notes

### Observed behavior

- Changed-manifest validation cleaned each Git path, matched the basename and
  pattern, then verified the physical file without stack-detection admission.

### Implementation

- `stackdetect.CanonicalDiscoveryPath` is the path-only admission API. Detect
  and assurance share ignored-directory and depth constants. No extra walk.
- Admissible deleted, symlinked, or non-regular manifests still fail closed.
  Ignored trees and over-depth paths never enter verification.

### Verification

- Table tests cover every ignored directory (first, middle, case), Windows
  separators, depth 6 vs 7, and non-canonical forms. Assurance tests cover
  deleted ignored manifests, admissible symlink rejection, and vendor
  non-impersonation. `go test -race ./internal/stackdetect ./internal/assurance`,
  `make lint`, and `make test` passed.

### Required scope distinction

- Admissible deleted manifest: still blocks because it may represent a removed
  real module boundary.
- Structurally ignored manifest: cannot represent a detected module and must not
  block merely because Git reports it changed.
- Changed source without any admissible detected module: retains the existing
  missing-module-evidence result.
- Workspace membership and explicit exclusions remain authoritative after path
  admission.

### Verification matrix

- Go, Rust, and Python manifest names at root and nested roots.
- Every ignored directory as first, middle, and case-varied path segment.
- Depth five, six, and seven around the existing definition.
- Existing regular file, deleted file, directory replacement, symlink, and
  outside-root spelling.
- Root workspace, nested workspace member, excluded member, overlapping root,
  and unrelated ignored manifest.

## Deviations

None.
