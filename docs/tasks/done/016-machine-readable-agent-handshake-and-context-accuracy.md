# TASK 016: Machine-readable agent handshake and context accuracy

## Why

Reconc already has the right agent-facing primitives, but the compact session briefing omits repository run state, its JSON has no explicit format version, and the context-size default counts optional historical documents while missing the canonical TASK entrypoints. Agents need one bounded, machine-readable orientation path without another mode, duplicated skill installation, or unnecessary startup tokens.

## Acceptance

- `reconc session-briefing . --json` exposes a versioned compact contract including repository run status without mutating state or invoking Git.
- `reconc context size .` measures canonical Reconc session entrypoints and the active TASK detail, excludes optional legacy planning files by default, deduplicates paths, and never rounds a non-empty file to zero tokens.
- Governed bootstrap guidance points agents to the compact machine-readable briefing and on-demand embedded guide without copying another skill into target repositories.
- README, product documentation, command reference, embedded guide, skill, architecture threat boundary, and bootstrap tutorial describe the same behavior and limits.
- Focused tests, full tests, race tests, vet, build, self-hosting, release trust, TASK validation, and final policy gates pass.

## Sub-Tasks

- [x] Version and enrich the compact agent briefing.
- [x] Correct context-budget inputs and accounting.
- [x] Align bootstrap, README, skill, guide, command, architecture, and product documentation.
- [x] Run the complete verification matrix and archive the TASK.

## Notes

- Golem is read-only. Its installed hook and policy files are evidence only and are not modified.
- A new installed skill or agent mode would duplicate guidance and increase drift. The existing embedded guide plus compact briefing remain the single operating path.
- The old context default measured 5,932 approximate tokens in this repository, including 2,626 tokens from ignored historical `docs/todo.md`, while omitting the active TASK. The final corrected default measures 1,312 approximate tokens including this expanded active TASK, a 4,620-token or 77.9% reduction.
- With no active TASK after archival, the same final command measures 678 approximate tokens, a 5,254-token or 88.6% reduction from the old default.
- Reconc cannot provide a hostile same-user security boundary. The README, product docs, architecture threat model, skill, and embedded guide now route adversarial enforcement to an external sandbox plus protected remote CI or branch rules.
- Verification passed for focused tests, root and template-harness full tests, both race suites, both vet runs, pinned Staticcheck v0.7.0, binary build, self-hosting, release trust, TASK validation, and read-only runtime output checks.
- Generator, registry, scaffold, native-shape, and self-host proofs cover all supported platforms. Existing installed hook artifacts were not changed. The installed Codex artifact in this repository and Golem remains intentionally visible as degraded because it still carries unsupported `SessionEnd` and lacks current per-route timeout contracts; Golem still executes its unchanged v0.5.0 binary.

## Deviations

None.
