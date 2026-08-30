# TASK 419: Consolidate bounded subprocess capture

## Why

Command proof and runtime subprocess paths carry local bounded-buffer implementations with different truncation flags and concurrency behavior even though `internal/boundedexec` already owns a mutex-safe bounded sink.

## Acceptance

- Subprocess stdout/stderr capture uses one reviewed bounded implementation wherever semantics are identical.
- Each caller preserves its exact byte ceiling, independent stdout/stderr accounting, returned prefix, and truncation error text.
- Concurrent stdout/stderr writes remain race-free.
- Allocation and concurrency tests prove equivalent behavior with no silent truncation.

## Sub-Tasks

- [x] Compare every local writer contract with `boundedexec.Buffer` before migration.
- [x] Migrate only semantically identical command-proof and runtime call sites.
- [x] Retain intentionally different silent or streaming writers with explicit rationale.
- [x] Run focused command-proof, runtime, boundedexec tests, and benchmarks.

## Notes

- Verified from finding 80.
- Verified five equivalent retained-prefix subprocess sinks in command proof, runtime Git, policy scripts, repository-sync Git, and Grok inspection. All return the input write length, retain an exact per-stream prefix, and discard only bytes beyond a positive constant ceiling.
- Policy scripts intentionally do not turn truncation into a policy error: the documented contract bounds retained stdout/stderr while exit status remains authoritative. The shared sink preserves that behavior because this caller deliberately does not inspect `Truncated`.
- Non-equivalent writers remain local: CLI tracked writers preserve downstream write errors, Grok's locked writer only serializes an unbounded stream, and MCP's strict frame writer enforces protocol framing.
- Focused tests passed for `internal/boundedexec`, `internal/commandproof`, `internal/runtime`, `internal/bootstrap`, and `internal/grokacp`; the final `make test-fast` passed.
- Apple M1 benchmark at 100 iterations: 4 KiB admitted capture measured 689.6 ns/op and 4,201 B/op with 2 allocations; already-full overflow writes measured 15.83 ns/op with 0 B/op and 0 allocations.

## Deviations
