# TASK 427: Serialize JSONL retention and validate modes

## Why

Repository byte-pressure retention removes JSONL archives without the writer's layout lock, while default-layout append/recovery accepts existing files and locks with foreign write bits.

## Acceptance

- Retention coordinates archive removal with the exact JSONL writer layout lock and identity.
- Default layouts reject group/world-writable live, archive, backup, journal, and lock files without widening stricter modes.
- Lock ordering is documented and deadlock-free across append, rotation, enforce, and retention.
- Deterministic races and Unix mode tests cover concurrent rotation, replacement, and insecure existing objects.

## Sub-Tasks

- [x] Expose the minimal validated maintenance boundary needed by retention.
- [x] Apply one mode-validation contract to default and custom layouts.
- [x] Add deterministic rotation/removal and permission regressions.
- [x] Run focused JSONL and retention tests.

## Notes

- Verified from findings 156 and 157.
- Reverification mapped the source findings to OMP findings 913 and 915. The
  repository-total path still removed run-decision archives under only the
  retention lock, and default-layout mode checks still skipped existing files.
- `WithLayoutMaintenanceContext` now recovers under the exact writer lease and
  supplies that lease only to bounded maintenance. Archive removal requires the
  discovered canonical identity and revalidates it through the rooted parent.
- Retention lock ordering is outer retention lock, then run-decision JSONL lock.
  Append, rotation, and recovery never acquire the retention lock.
- One mode validator preserves restrictive default modes, rejects Unix
  group/world write bits, retains exact custom-layout modes, and leaves Windows
  security enforcement to the layout security implementation.
- Focused verification passed: `go test ./internal/jsonl ./internal/retention
  -count=1` and `git diff --check`.

## Deviations
