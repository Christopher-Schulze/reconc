# reconc v0.9.8

Reconc v0.9.8 is a broad hardening release for the repository, action, runtime,
and release boundaries. It keeps every public schema and policy-lock format
stable while making filesystem mutation, retained state, subprocess control,
MCP enforcement, and public reporting substantially more defensive and less
expensive.

## Security and privacy

- Filesystem-sensitive operations now bind reads, writes, removals, recovery,
  and ownership changes to validated opened identities. Parent replacement,
  escaping symlinks, unsafe hard links, inode-split locks, and stale path
  observations fail closed across bootstrap, uninstall, update, audit, JSONL,
  command proof, action state, and retention paths.
- Public proofs, CI reports, impact reports, adapter diagnostics, and command
  summaries redact host paths, user identities, quoted sensitive values, and
  unsafe tool data at their output boundaries. Public impact actions no longer
  expose composed absolute paths.
- Strict YAML and JSON admission rejects ambiguous merge semantics, duplicate
  keys, oversized aliases, malformed configurations, and unsafe dynamic
  launcher arguments before trusted structures are created.
- Runtime executables, policy scripts, bootstrap artifacts, receipts, and
  embedded harness packs are rebound to their validated bytes and provenance
  immediately before use or publication.

## Durable state and transactions

- Atomic publication reports whether bytes were not published, published with
  uncertain durability, or durably published. Close, sync, validation, and
  cleanup failures can no longer be flattened into success.
- Bootstrap and repository-sync staging, rollback, recovery, and removal retain
  parent handles and exact before/after identities. Interrupted operations
  preserve recoverable state without overwriting foreign or drifted bytes.
- Audit, run, action-ledger, session, and general JSONL storage share bounded,
  cancellable locking and strict mode validation. Rotation and recovery retain
  valid data, prevent lock-inode splits, serialize maintenance safely, and
  preserve terminal reasons and incomplete lifecycle evidence.
- Action reservations, approvals, checkpoints, and pending correlations now
  settle or expire deterministically across cancellation, restart, partial
  failure, and concurrent readers.

## Action and MCP enforcement

- Action evaluation owns one end-to-end deadline covering normalization, cache
  lookup, conditions, selectors, globs, inspection, budgets, traces, and final
  publication. Cancellation and deadline expiry win before a decision can be
  cached or returned.
- Tool selectors, budget selectors, namespaced identities, request envelopes,
  progress events, results, and strict audit records receive explicit bounded
  validation. Malformed upstream requests are isolated without corrupting the
  remaining MCP connection.
- Gateway shutdown drains calls and pending approvals in a stable order,
  preserves independent cleanup failures, stops progress admission, and never
  signals an already reaped Unix process group.
- Validated compiled action plans, matcher contexts, and immutable identity
  reads are safely reused; concurrent action-state reads no longer serialize
  behind unrelated read-only work.

## Runtime and agent integrations

- Session evidence, pre-decision state, taint resolution, Stop fingerprints,
  completion drift, compaction recovery, and hook liveness remain consistent
  across retries, restarts, terminal transitions, and interrupted persistence.
- Generated adapters fail closed on truncated output, sanitize diagnostics,
  construct MCP envelopes consistently, and retry hook-worker requests without
  duplicating committed effects.
- Kimi managed blocks are parsed structurally, mixed hook configuration keeps
  unrelated ownership intact, and partial wrapper installation is reported as
  partial instead of healthy.
- TUI width is measured in terminal cells, preserving CJK, combining, emoji,
  keycap, flag, variation-selector, and ZWJ clusters without corrupt output.

## Performance and bounded work

- Runtime plans cache immutable command expectations and validated action
  programs. Evaluations reuse matcher contexts, short-circuit conditions,
  precompiled inspection programs, source-freshness snapshots, Git state,
  executable verification, and Stop inputs.
- Canonical serialization, lockfile encoding, action traces, session identity,
  glob expansion, and evidence normalization avoid redundant allocations and
  full-input passes.
- Run-log following decodes only validated appended suffixes; artifact and
  executable verification stream bounded inputs; subprocess output uses shared
  retained-prefix capture; impact filesystem snapshots and Grok continuation
  prompts have explicit work and memory ceilings.

## Platform and release reliability

- Windows private state applies and validates current-user-only DACLs through
  opened handles, preserves write-through replacement semantics, and verifies
  bootstrap modes through native contracts. The final native Windows suite and
  installer failure paths remain release-blocking.
- Offline hook verification is hermetic, self-host fixtures stay synchronized
  with current runtime contracts, and generated harness assets remain bound to
  the embedded pack.
- CI, release publication, and CodeQL diagnostics preserve complete blocking
  findings under bounded output and avoid leaking hosted-runner paths.

## Compatibility

- The product version advances to `0.9.8` without a policy-lock format or
  public JSON-contract change.
- Format-6 locks continue to use the immutable
  `reconc-v0.9.7/schemas/v6/policy-lock.schema.json` identity. Formats 1 through
  5 and every other registered schema retain their exact published URLs,
  bytes, aliases, and migration behavior.
- Existing policy locks, repository installation receipts, runtime adapters,
  and direct installations require no manual data migration.

## Verification

The protected release workflow validates the exact tag with native macOS,
Linux, and Windows tests; root and portable-template race suites; Vet,
Staticcheck, Govulncheck, CodeQL, publication audit, release trust, self-hosting,
the pinned external LangChain MCP proof, five release targets, strict manifests,
checksums, deterministic SBOMs and notices, and GitHub build-provenance
attestations.

## Upgrade

Use the existing installation owner after publication:

```bash
reconc update
reconc doctor --global
```

No repository lockfile refresh is required for this release.
