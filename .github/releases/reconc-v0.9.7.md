# reconc v0.9.7

Reconc v0.9.7 is the source candidate for the next immutable release. It
keeps the format-6 policy-lock representation and migration chain stable while
publishing the rule-kind field matrix under a new schema identity.

## Schema and compatibility

- The immutable release will publish Format 6 as
  `https://raw.githubusercontent.com/Christopher-Schulze/reconc/reconc-v0.9.7/schemas/v6/policy-lock.schema.json`.
- The schema rejects rule fields that are unsupported by the declared rule kind
  before runtime evaluation.
- Existing format-6 locks naming the v0.9.6 schema remain accepted through an
  input-only compatibility alias; refreshing a lock emits the v0.9.7 identity.
- Formats 1 through 5 retain their exact previously published schema URLs and
  migration behavior. No format-version migration is required.
- Unchanged public schema contracts remain byte-identical at their existing
  immutable v0.9.6 identities.

## Go 1.27 toolchain and implementation

- The root module and portable harness build with stable Go 1.27. Native macOS
  builds consequently require macOS 13 Ventura or later.
- Strict action-value and lockfile decoding use Go 1.27's stable
  `encoding/json/jsontext` token API while retaining Reconc's duplicate-key,
  Unicode, depth, cardinality, decimal, trailing-input, and error contracts.
- Canonical action values append into one capacity-hinted output buffer while
  preserving identity-bearing legacy escaping. Targeted in-memory concurrency
  tests use `testing/synctest`, and MCP shutdown regression coverage queries the
  exact `goroutineleak` worker stack without treating unrelated goroutines as
  product leaks.

## Transaction and filesystem reliability

- Transactional JSONL publication recovers interrupted commits and preserves a
  private on-disk layout for audit evidence.
- Atomic state, lock, bootstrap, and audit publication verifies parent and open
  file identities rather than trusting path names across filesystem races.
- Production file-lock acquisition is bounded, cancellation-aware, and backed
  by a lifecycle-managed same-process gate without changing the authoritative
  cross-process deadline.
- Policy sources and TASK files are read through stable file identities, with
  path components revalidated across multi-read operations.
- Hook workers fail closed on oversized frames, fall back safely, and restart
  after crashes or canceled requests without retaining a failed process.

## Compiler and policy-ingest correctness

- One locked policy-source snapshot, discovery context, configuration parse,
  provenance computation, and normalized rule representation now flow through
  compilation without redundant whole-input work.
- Policy-source precedence is canonical and reported truthfully across
  repository instructions, policy files, and generated inputs.
- Glob expansion, inline-policy extraction, parser cardinality, source text,
  and template variables are bounded before materialization or retention.
- Unsupported rule fields and invalid template-variable grammar are rejected at
  their owning boundaries.
- Strict lockfile loading preserves duplicate-key, Unicode, depth, number,
  root-shape, trailing-data, migration, freshness, and digest checks while
  caching validated typed rule and action parts for downstream compilation.

## Runtime performance and bounded work

- Runtime path and template matchers plus expected shell invocations are
  precompiled; command evidence and stable evidence-file snapshots are
  normalized or memoized once per evaluation.
- Runtime-plan cache hits revalidate policy-source freshness before reuse.
- Prospective path identities and write-epoch normalization are batched, while
  require-script preparation, evidence matching, package-manager ancestry, and
  action argument sizing avoid repeated work.
- Stop evaluation reuses one bounded lockfile scan and explicit before/after
  source snapshots while retaining mutation detection and fail-closed cache
  eligibility.
- Immutable action plans and action context roots are validated once and reused
  safely within an evaluation.
- Runtime evidence deduplication and inline-policy extraction use bounded linear
  algorithms. Worker response frames use bounded geometric growth instead of
  repeated prefix copies.
- Context-size accounting is bounded and overflow-safe. Harness-pack payload
  limits are enforced before values are retained.

## Evidence, review, and privacy hardening

- Action deltas distinguish warned from blocked operations, and assurance-file
  reads expose one stable bounded snapshot.
- Extracted rule identifiers are collision-resistant, and proof bundles expose
  only sanitized executable identities; command hashes do not encode raw
  arguments or complete command lines.
- Lockfile diffs deterministically report semantic, provenance, generated,
  unsupported, envelope, source-inventory, and rule-provenance changes,
  including review-relevant generated-only changes.
- Generated harness and publication artifacts remain derived from their
  canonical sources.

## Verification

The candidate is validated by strict-decoder differential and fuzz coverage,
runtime and worker contract tests, race tests, static analysis, schema and
publication audits, harness-pack verification, self-hosting, release trust,
the pinned external LangChain interoperability proof, vulnerability scans, and
the complete five-target release build. These checks establish the local source
candidate only. Remote schema and release-asset HTTP checks must be run by the
protected, tag-bound release workflow after `reconc-v0.9.7` is published.

## Upgrade

After the immutable release is published, use the existing installation owner:

```bash
reconc update
reconc doctor --global
```

Repositories with an existing format-6 lock do not need a migration. Run
`reconc refresh .` when intentionally rewriting the lock with the v0.9.7
schema identity and review the policy source and lockfile together.
