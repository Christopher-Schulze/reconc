# TASK 427: Serialize JSONL retention and validate modes

## Why

Repository byte-pressure retention removes JSONL archives without the writer's layout lock, while default-layout append/recovery accepts existing files and locks with foreign write bits.

## Acceptance

- Retention coordinates archive removal with the exact JSONL writer layout lock and identity.
- Default layouts reject group/world-writable live, archive, backup, journal, and lock files without widening stricter modes.
- Lock ordering is documented and deadlock-free across append, rotation, enforce, and retention.
- Deterministic races and Unix mode tests cover concurrent rotation, replacement, and insecure existing objects.

## Sub-Tasks

- [ ] Expose the minimal validated maintenance boundary needed by retention.
- [ ] Apply one mode-validation contract to default and custom layouts.
- [ ] Add deterministic rotation/removal and permission regressions.
- [ ] Run focused JSONL and retention tests.

## Notes

- Verified from findings 156 and 157.

## Deviations
