# TASK 004: State retention and SSD hygiene

## Why

Session state is rewritten in full for every mutation, command evidence grows
without a hard bound, and existing pruning is harness-local and count-based.
Long sessions therefore create avoidable write amplification, stale state,
large reports, append-only decision logs, and temp/cache residue.

## Acceptance

- Unchanged session and active-session state is never rewritten.
- Repeated evidence is deduplicated and every persisted collection has a deterministic byte or item bound.
- Core pruning covers sessions, reports, locks, audit logs, runloop decisions, generated audit binaries, and abandoned temp files.
- Retention enforces per-class and total byte budgets, age limits, atomic cleanup, and safe active-file exclusions.
- Cheap lifecycle-triggered pruning works even when the harness audit cache never runs.
- Tests measure bounded growth, write counts, crash safety, concurrency, and cleanup correctness.

## Sub-Tasks

- [ ] Define storage classes, byte budgets, age limits, and active-file invariants.
- [ ] Add write-on-change state publication and bounded evidence compaction.
- [ ] Move pruning into the product core and cover every runtime state class.
- [ ] Wire cheap interval and lifecycle triggers without Stop-path friction.
- [ ] Prove SSD-write, size, race, and crash-safety behavior.

## Notes

Approved areas: 10 Audit retention regression. The current `~/.reconc` sample
contains full-file rewrites and files above one megabyte, so count-only pruning
is not an acceptable bound.

## Deviations

None.
