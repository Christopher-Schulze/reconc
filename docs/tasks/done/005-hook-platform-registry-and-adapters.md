# TASK 005: Hook platform registry and adapters

## Why

Hook behavior is distributed across platform-specific generators and runtime
switches. Coverage, event semantics, failure policy, activation requirements,
timeouts, context compaction, and diagnostics need one explicit capability
model so new platforms remain thin and predictable.

## Acceptance

- A typed platform registry owns supported events, native capabilities, fallbacks, failure semantics, timeout budgets, config paths, and activation probes.
- Claude Code, Codex, Cursor, OpenCode, Devin, Antigravity CLI, GitHub Copilot, and Kilo adapters are generated from or validated against that contract.
- Devin includes supported compaction lifecycle handling and Antigravity uses the current `.agents/hooks.json` contract.
- OpenCode and Kilo host integrations stay thin and delegate policy decisions to the Go binary.
- Bootstrap and doctor report installed, active, degraded, shadowed, and unsupported states truthfully.
- Hook output and context packets are bounded, deduplicated, actionable, and benchmarked.

## Sub-Tasks

- [x] Model the common event and capability contract.
- [x] Refactor existing adapters onto the registry without behavior drift.
- [x] Add and verify Devin, Antigravity, Copilot, and Kilo coverage.
- [x] Add activation probes, exact diagnostics, and bounded latency/failure policy.
- [x] Run native-shape fixtures, smoke tests, benchmarks, and docs verification.

## Notes

Approved areas: 15 Hook coverage; 16 Hook contract core/capability registry;
17 Thin OpenCode/Kilo adapters; 18 Hook latency budgets/fail semantics;
24 Metrics/activation truth.

Verified source delta: Golem adds a first-class Devin hook artifact, payload
normalization, stable fallback session identity, and duplicate suppression when
Devin also loads compatible Claude hooks. Its permanent project run-loop prompt
and hard-coded old Darwin release lookup are project-specific regressions and
must not be copied. Standalone Reconc already has the stronger portable binary
resolver, bounded state, Stop cache, and retention implementation.

Verified current platform contracts: Devin uses `.devin/hooks.v1.json`, exit 2
for blocking lifecycle hooks, and `PostCompaction` for bounded context recovery;
Antigravity uses `.agents/hooks.json` and camel-case tool payloads; Copilot uses
repository-local `.github/hooks/*.json` version 1 files and platform-specific
decision responses; Kilo auto-loads project plugins from `.kilo/plugin/` while
OpenCode auto-loads `.opencode/plugins/`.

Current Claude Code exposes `PostCompact` but cannot consume context or control
output there. Reconc therefore routes the context-capable
`SessionStart(compact)` event to the neutral post-compaction handler and emits
the registry's 5-second budget instead of accepting Claude's long default.

Design: one immutable typed registry owns platform order, target/config paths,
normalized event coverage, native event names, support mode, error and timeout
failure policy, timeout budget, activation mode, generator, and installer.
Bootstrap, scaffold sync, runtime dispatch help, and diagnostics consume that
registry. Diagnostics distinguish absent, installed, active, degraded,
shadowed, and unsupported configuration state; `active` means configuration is
discoverable, not that a live process proved it loaded the file.

Implemented nine registry-owned artifacts, registry-driven bootstrap/runtime
help/scaffold sync, non-destructive installers, activation status, Devin and
Copilot payload/result adapters, duplicate suppression, bounded compaction
context, and thin OpenCode/Kilo transports. Combined runtime output is capped
at 8 KiB including truncation markers.

Verification: root `go test ./...`, `go vet ./...`,
`go test -race -count=1 ./...`, and `make lint` pass; the template harness
passes `go test ./...`, `go vet ./...`, and race tests. Generated JSON parses,
the git hook passes `sh -n`, both Bun plugins pass `bun --check`, and a fresh
nine-artifact scaffold smoke test passes. Runtime route lookup measures
7.375 ns/op with zero allocations; full eight-agent artifact generation
measures 230356 ns/op.

## Deviations

None.
