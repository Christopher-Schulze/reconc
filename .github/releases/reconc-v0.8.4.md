# reconc v0.8.4

`v0.8.4` makes compiled policy portable across equivalent clones and replaces
timestamp-based repo-binary freshness with deterministic embedded provenance.
It also closes the product-wide project-state retention gap found during a
full Omnimus workflow proof run.

## Portable policy lockfiles

- Current policy lockfiles use format 2 and the public v2 schema.
- Physical checkout paths are replaced by the portable `.` root marker, so
  equivalent clones and worktrees compile byte-identical lockfiles.
- Immutable format-1 lockfiles remain recognized, schema-identity-validated,
  and migrated in memory without modifying their input.
- Malformed, unknown, schema-drifted, and non-portable current envelopes fail
  closed with explicit refresh remediation.

## Deterministic build provenance

- Host and release binaries embed version, GOOS, GOARCH, and a deterministic
  digest of target-selected production sources and embedded assets.
- Build targets inspect finished binary bytes and reject missing, malformed,
  wrong-target, wrong-version, or stale-source provenance without executing
  the candidate.
- Harness workflow audits use the same byte-only inspection and no longer
  trust filesystem modification times.
- `reconc version --json` exposes the embedded target and source digest.

## Bounded storage

- Product retention now bounds the global recognized project-state set to 256
  roots, 128 MiB, and 30 days in addition to all existing per-project limits.
- Explicit prune enforces the global limit immediately. Lifecycle pruning
  preserves the current project, live sessions, unknown directories, and a
  24-hour recent-activity concurrency grace.
- Global project and owned-temp scans remain serialized and rate-limited, so
  the added bound does not turn every session start into a full disk walk.

## Grok enforcement hardening

- Managed Grok activation and ACP preflight require generator-exact hook and
  executable wrapper artifacts, project-owned inspect metadata, and all 14
  exact route command tokens.
- PreToolUse runs through a bounded guard; only one exact allow/deny JSON object
  passes through, while crashes, timeouts, empty output, multiline output, and
  malformed decisions become explicit deny JSON.
- Leader steering counts only successfully delivered interjections toward its
  32-attempt no-progress cap; transport and protocol failures do not consume
  enforcement budget.
- Framed ACP and leader writes complete short writes, cancellation terminates
  cleanly, and deep doctor separates inspect output while verifying protocol
  version 1 and `_x.ai/interject`.

## Release assets

- Five platform binaries
- Bash, Zsh, and Fish completions
- Man page
- Four immutable v1 schemas plus the current v2 policy-lock schema
- SPDX 2.3 and CycloneDX 1.6 SBOMs
- `SHA256SUMS`

No tag, GitHub release, or remote publication is created by the source commit.
