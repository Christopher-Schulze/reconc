# TASK 160: Record a privacy-bounded action decision ledger

## Why

Operators need to explain what Reconc decided, which policy matched, whether the
call executed, what approval or budget state was consumed, and whether the result
was contained. Storing full tool arguments and results would create a new secret
and personal-data repository, while reusing the generic audit entry shape could
silently leak fields it was not designed to bound.

The Action Plane needs its own versioned event schema built on Reconc's verified
hash-chain and retention machinery. The product must describe this honestly as a
tamper-evident retained ledger, not immutable or permanent storage.

## Acceptance

- Action ledger format 1 has typed event variants for request accepted,
  pre-decision, approval transition, budget reservation and settlement,
  downstream dispatch and outcome, result inspection, final delivery, and
  terminal failure.
- Each event binds Reconc call ID, optional keyed upstream request identity,
  repository identity, policy and lock digests, server label and fingerprint,
  exact tool name or configured redacted tool identity, tool-contract digest,
  operator-bound principal, safe credential labels, run and session correlation
  identities, phase, decision, stable reason codes, rule IDs, context provenance,
  completeness, timestamps, and latency.
- Argument and result evidence is limited to policy-selected field digests,
  bounded safe labels, byte and item counts, detector categories, schema status,
  approval receipt ID, budget deltas, and execution status. Raw arguments,
  results, headers, credential values, environment values, and arbitrary MCP
  metadata are forbidden by type and validation.
- Selected-field digests are domain-separated HMAC-SHA-256 values produced by
  the TASK 157 key owner. The ledger omits the digest and marks evidence
  incomplete if keyed identity is unavailable; it never writes a plain digest
  of a low-entropy value.
- The ledger reuses shared hash-chain primitives where safe but has a separate
  schema, path, decoder, validator, renderer, and compatibility contract.
- Append is atomic and serialized across processes; partial writes, concurrent
  writers, rotation, crash, disk full, permission loss, symlink replacement,
  FIFO, device files, oversized records, malformed prior tails, and hash mismatch
  produce explicit fail-closed or configured observation-failure behavior.
- Retention is a bounded live file plus bounded archives and detached chain-head
  evidence. Pruning cannot erase a protected active transaction or claim a chain
  is complete when archives are missing.
- Verification reports retained-chain integrity, archive continuity, detached
  head status, first and last retained sequence, dropped-history boundary, and
  event completeness separately.
- `reconc action log tail`, `stats`, `verify`, and `export` are registry-owned,
  bounded, filterable by safe identifiers, and deterministic in text and JSON.
  Stats group explicit run/session/principal/tool lifecycles without treating an
  MCP connection as a durable session or inventing missing terminal events.
- Export supports privacy-preserving Impact Lab action cases but never expands
  digests back into raw values or claims replay completeness without sufficient
  evidence.
- Ledger write failure behavior is part of action policy. A policy requiring
  verified recording fails closed before dispatch if a durable pre-decision
  event cannot be committed.
- Status surfaces distinguish evaluated, approved, dispatched, downstream
  succeeded/failed/unknown, result delivered/withheld, and incomplete terminal
  calls without inferring success from missing events.
- Schema, tamper, rotation, concurrency, crash, retention, privacy, export,
  correlation, malformed-state, and mutation tests cover every event and
  transition.

## Sub-Tasks

- [x] Inventory current audit, JSONL, retention, privacy, session, MCP audit, and
      policy-proof implementations and isolate reusable primitives
- [x] Define action-ledger format 1 event variants and the allowed field matrix
      for every lifecycle transition
- [x] Make raw arguments, results, secrets, headers, and arbitrary metadata
      unrepresentable in ledger domain types
- [x] Reuse TASK 157 keyed selected-field identity with domain separation,
      canonical values, policy binding, rotation metadata, and no plain-hash
      fallback
- [x] Define call lifecycle states and terminal completeness without inferring
      missing dispatch or result events
- [x] Define run and session correlation, aggregation, and incomplete-session
      semantics without inventing timeout closure or using MCP connection lifetime
- [x] Implement a separate bounded path, strict decoder, validator, appender,
      rotation, archive continuity, and detached-head verification
- [x] Reuse hash-chain and JSONL primitives only after exact concurrency,
      transaction, and privacy invariants are proven compatible
- [x] Serialize multi-process appends and preserve monotonic sequence and chain
      identity across crashes and rotation
- [x] Define ledger-required pre-dispatch failure results and the exact budget
      release or indeterminate lifecycle ordering consumed by TASK 161
- [x] Prove typed event construction and ordering for evaluator, budget,
      approval, result-inspection, and future TASK 161 gateway boundaries
      without double-recording
- [x] Register `action log tail`, `stats`, `verify`, and `export` in the command
      catalog before adding dispatch or docs
- [x] Implement bounded filters, deterministic renderers, stable JSON, safe
      summaries, and explicit retained-history boundaries
- [x] Export privacy-bounded format-2 Impact Lab cases only when evidence is
      sufficient; mark every absent dimension incomplete
- [x] Add retention ownership so generic pruning cannot remove active protected
      state or silently break archive continuity
- [x] Add type-level and runtime privacy tests that scan serialized events for
      synthetic raw secrets, arguments, outputs, headers, and environment values
- [x] Add tamper, truncation, reorder, duplicate, archive-gap, detached-head,
      partial-write, concurrent-writer, crash, disk-full, symlink, FIFO, device,
      and oversized-record tests
- [x] Add lifecycle tests for allow, warn, block, approval, timeout,
      cancellation, downstream crash, unknown outcome, result withhold, and
      ledger-required failure
- [x] Add fuzz targets for decoding, verification, filtering, export, and
      malformed archive sets
- [x] Add mutation tests proving every lifecycle event, privacy prohibition,
      chain input, completeness field, and retention guard is enforced
- [x] Update RFCs, schemas, architecture, documentation, commands, retention,
      Impact Lab, privacy, status, and publication surfaces
- [x] Re-read every modified file and run focused tests, race tests, complete
      module gates, static analysis, coverage, and publication audits

## Notes

Depends on TASK 155, TASK 157, TASK 158, and TASK 159 contract fields. It can be
implemented before the MCP gateway by driving events from deterministic
fixtures, then TASK 161 integrates the live transport boundary.

The ledger is local evidence. Its hash chain detects modification within the
retained evidence set; it does not prevent deletion by an actor who controls the
filesystem and detached head.

Inventory result: `internal/audit` owns the proven retained hash-chain and
detached-head semantics, while `internal/jsonl` owns cross-process serialization,
rotation, and append recovery. Their current public shapes are not directly safe
for the Action Ledger: generic audit entries permit payload-adjacent fields, and
JSONL derives public-mode lock/journal paths that do not match the private ledger
contract. `internal/actionstate` already owns the private 0700/0600 project action
directory, key lease, keyed run/session identities, retention coordination, and
symlink/special-file checks. The ledger therefore needs its own payload-free
schema and validation, a narrowly reusable private project-storage boundary, and
an exact-path private JSONL transaction configuration instead of copying the
generic audit entry type or weakening the RFC paths.

The implementation keeps live transport orchestration in TASK 161. TASK 160
owns the closed event types, recording-mode result, strict lifecycle ordering,
private storage, query, verification, and export contracts that the gateway
must call. It does not invent run or session closure from inactivity or an MCP
connection ending.

Final audit tightened selected-field identities with repository and declaration
binding, canonical approval request and receipt IDs, decision/reason
compatibility, phase/source ownership, active-call retention, completeness
evaluation truth, permanent lock-error handling, and Windows reparse-point
rejection. It also made publication-test secrets structurally synthetic and
documented five exact immutable-history fixture exceptions without weakening the
current-tree scanner.

Final proof: focused and complete race suites, Windows cross-compilation,
release-trust, publication audit, root and template coverage, self-hosting,
build, vet, pinned staticcheck, and pinned govulncheck all pass. Whole-module
coverage was measured for both the root and portable template modules as review
evidence, not as a pass/fail threshold.

## Deviations

None.
