# TASK 419: Consolidate bounded subprocess capture

## Why

Command proof and runtime subprocess paths carry local bounded-buffer implementations with different truncation flags and concurrency behavior even though `internal/boundedexec` already owns a mutex-safe bounded sink.

## Acceptance

- Subprocess stdout/stderr capture uses one reviewed bounded implementation wherever semantics are identical.
- Each caller preserves its exact byte ceiling, independent stdout/stderr accounting, returned prefix, and truncation error text.
- Concurrent stdout/stderr writes remain race-free.
- Allocation and concurrency tests prove equivalent behavior with no silent truncation.

## Sub-Tasks

- [ ] Compare every local writer contract with `boundedexec.Buffer` before migration.
- [ ] Migrate only semantically identical command-proof and runtime call sites.
- [ ] Retain intentionally different silent or streaming writers with explicit rationale.
- [ ] Run focused command-proof, runtime, boundedexec tests, and benchmarks.

## Notes

- Verified from finding 80.
- The task does not assume every capped writer is interchangeable; callers with intentional pretend-success semantics stay separate unless the contract is first changed explicitly.

## Deviations
