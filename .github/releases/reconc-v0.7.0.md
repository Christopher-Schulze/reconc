# reconc v0.7.0

`v0.7.0` turns Reconc into a reusable, agent-native repository control plane:
one deterministic Go binary, one durable repository-run switch, transactional
bootstrap, typed TASK state, bounded evidence and storage, and native wiring
for the current coding-agent ecosystem.

## Highlights

- Replaces prompt-scoped runloop/degen modes with one durable
  `reconc run on|off|status|log` contract. It continues executable TASK work
  across messages, sessions, interrupts, runtime changes, and application
  restarts until the TASK workflow is exhausted or an agent explicitly runs
  `reconc run off`.
- Adds deterministic `minimal`, `governed`, and `existing` bootstrap profiles
  with inspect, plan, apply, verify, rollback, drift candidates, binary
  resolution, and create-only publication.
- Adds a typed TASK lifecycle with validation, claim, block, resume, split,
  promotion, archive, crash recovery, and a compact machine-readable
  `session-briefing` handshake.
- Ships a typed hook registry and generated integrations for Git pre-commit,
  Claude Code, Codex, Cursor, OpenCode, Devin CLI, Antigravity CLI, GitHub
  Copilot, and Kilo Code.
- Adds versioned policy packs and bounded native assurance gates for repository
  layout, generated references, dependency pins, language boundaries, network
  and process controls, substantive proof, live verification, Go concurrency,
  Go formatting, and source hygiene.

## Performance And Storage

- Routine executable-TASK Stop handling now returns before full policy-report
  construction, spawns no Git process, and performs no write for disabled or
  unchanged run events.
- On Apple M1, the in-process executable-TASK benchmark improved from
  `1,504,653 ns/op` to a `131,483 ns/op` median: about `11.4x` faster and
  `91.3%` lower latency. Allocations fell from `553` to `245` (`55.7%`), and
  allocated bytes fell from `61,612` to `29,225-29,276 B` (`52.5%`). Process
  startup is intentionally excluded from this benchmark.
- Stop fingerprints reuse one bounded Git status snapshot, direct ref
  resolution, dirty-path hashes, and evidence-aware report caching instead of
  full binary diffs and repeated repository walks.
- Session, report, lock, audit, decision, cache, generated-binary, repository,
  and external-state budgets now have explicit byte, count, age, rotation, and
  cleanup limits. Unchanged state uses write-on-change publication.
- Workflow-audit builds use atomic publication and cross-process locks;
  independent cold cache keys execute concurrently without compiler stampedes
  or cache overwrite races.

## Reliability And Security

- Read-only inspection and enforcement no longer refresh policy implicitly.
  Missing or stale lockfiles fail closed and point to `reconc refresh .`.
- Lockfiles, public JSON artifacts, bootstrap plans, and TASK transactions use
  deterministic, versioned contracts with strict validation and bounded input.
- Hook payloads are size, depth, output, and timeout bounded. Write and shell
  gates fail closed; observation-only routes avoid unnecessary work.
- Audit and installer paths verify checksums, reject malformed or ambiguous
  inventories, preserve existing installations on failure, and test negative
  paths explicitly.
- The tagged-release workflow enforces source/tag parity, repeats the complete
  release gate, publishes SHA-256 manifests, and creates GitHub build-provenance
  attestations before releasing artifacts.

## Upgrade Notes

- Replace any `/runloop`, degen-mode, or prompt-continuation integration with
  `reconc run on .` and `reconc run off .`.
- Use `reconc refresh .` after policy sources change. Inspection commands no
  longer compile policy as a side effect.
- Re-run a reviewed bootstrap plan to update Reconc-owned hook artifacts. The
  generated wrapper is release-version agnostic and fails closed on ambiguous
  compatible binaries.
- Existing repositories should use `--profile existing`; it updates only the
  explicitly selected hooks, wrapper, and optional binary while preserving the
  repository's policy, docs, TASK state, and ignore rules.

## Release Artifacts

- `reconc-0.7.0-darwin-amd64`
- `reconc-0.7.0-darwin-arm64`
- `reconc-0.7.0-linux-amd64`
- `reconc-0.7.0-linux-arm64`
- `reconc-0.7.0-windows-amd64.exe`
- Bash, Zsh, and Fish completions
- man page
- three public v1 JSON schemas
- `SHA256SUMS`
