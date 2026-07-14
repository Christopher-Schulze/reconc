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

- [ ] Model the common event and capability contract.
- [ ] Refactor existing adapters onto the registry without behavior drift.
- [ ] Add and verify Devin, Antigravity, Copilot, and Kilo coverage.
- [ ] Add activation probes, exact diagnostics, and bounded latency/failure policy.
- [ ] Run native-shape fixtures, smoke tests, benchmarks, and docs verification.

## Notes

Approved areas: 15 Hook coverage; 16 Hook contract core/capability registry;
17 Thin OpenCode/Kilo adapters; 18 Hook latency budgets/fail semantics;
24 Metrics/activation truth.

## Deviations

None.
