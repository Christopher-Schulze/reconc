# TASK 014: Hook reality and Stop hotpath

## Why

The eight agent adapters need contract-accurate activation reporting and generated configuration without mutating live host configuration during verification. The repository Stop path began syscall-dominated, carried removed runloop compatibility state, and performed avoidable filesystem work on every autonomous continuation.

## Acceptance

- Claude Code, Codex, Cursor, OpenCode, Devin CLI, Antigravity CLI, GitHub Copilot, and Kilo Code adapters are verified against current host contracts and local runtime evidence without installing or activating hooks.
- Generated Codex activation uses the valid `features.hooks` TOML section and status rejects lookalike or invalid placements.
- Configuration discovery, live execution evidence, inferred Stop support, and unsupported lifecycle events are reported without overclaiming.
- Repository run state uses `.reconc/run/` only; removed session-runloop modes, marker cleanup, and compatibility state are absent from current product code and docs.
- The normal executable-TASK Stop hotpath performs no Git process and removes avoidable root resolution, parsing, allocation, state publication, and routine decision-log overhead while preserving cross-process correctness.
- Benchmarks record reproducible before/after latency and allocation evidence; C/cgo is excluded unless measurement proves a material advantage.
- Tests, race tests, vet, static analysis, build, self-hosting, and release-trust checks pass.

## Sub-Tasks

- [x] Verify all eight adapter contracts, generated routes, activation semantics, and live evidence.
- [x] Correct hook generation and status truth without touching active hook configuration.
- [x] Remove historical runloop storage and compatibility semantics from current product surfaces.
- [x] Profile and optimize the Stop hotpath with measured before/after evidence.
- [x] Update documentation and complete the full verification matrix.

## Notes

- Baseline on Apple M1: 1,504,653 ns/op, 61,612 B/op, 553 allocs/op over a 5-second benchmark run.
- CPU profile: 93.6% of sampled CPU time is inside raw syscalls. Repeated root resolution, atomic run-state publication, decision-log publication, and TASK lifecycle parsing dominate.
- Codex CLI 0.144.4 rejects repository-root `hooks=true`; the supported placement is `hooks = true` under `[features]`.
- Static hook discovery now reports `configured`, while the current repository has no persisted `last_seen` evidence for the eight agent runtimes.
- OpenCode and Kilo expose `session.idle`, not a synchronous host Stop gate; autonomous continuation is inferred and fail-open at the host boundary.
- Optimized Apple M1 benchmark: 130,819-142,849 ns/op across seven 1,000-iteration runs, median 131,483 ns/op, 29,225-29,276 B/op, and 245 allocs/op after the final header-and-payload CRC hardening.
- Current repository status: Claude Code, Cursor, OpenCode, Devin CLI, Antigravity CLI, GitHub Copilot, and Kilo Code are statically configured. Codex status reports unsupported `SessionEnd` and missing route budgets; independent config inspection also finds invalid root-level activation placement. No active hook file was modified.
- Verification passed: root and harness `go test ./...`, `go test -race -count=1 ./...`, `go vet ./...`, Staticcheck v0.7.0, `make self-host`, and `scripts/tests/release-trust.sh`.

## Deviations

None.
