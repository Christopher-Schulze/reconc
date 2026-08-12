# TASK 165: Restore immutable schema and RFC contract truth

## Why

The Action Plane cannot add public contracts on top of a schema registry whose
local bytes, declared `$id`, runtime URL constants, release tags, and published
locations disagree. A source-first comparison on 2026-08-10 found 22 local JSON
schemas. Only `schemas/v4/policy-lock.schema.json` is byte-identical to the file
at its claimed release tag.

All 14 local v1 schemas that also exist at `reconc-v0.9.1` currently differ in
bytes from that immutable tag. Some differences are `$id` repairs, while others
are semantic additions such as MCP platform coverage, `cache_inputs`, and large
repository-sync expansions. Five additional v1 schemas do not exist in that tag
and use `https://reconc.dev/...` IDs; the domain did not resolve during the
audit. The v3 policy-lock URL names `reconc-v0.9.1`, where the file does not
exist, and local v2 bytes also differ from the file at its claimed tag.

RFC 0001 independently says format 4 in its header but format 3 in its required
fields table. These are released-contract truth defects, not Action Plane design
choices, and must be repaired before format 5 or new action schemas are emitted.

## Acceptance

- A complete machine-readable registry owns every public schema artifact and
  version independently: artifact name, schema version, format version where
  applicable, local path, immutable default URL, introduction tag, canonical
  SHA-256, compatibility status, and current/legacy state.
- Every one of the 22 inherited schema files is classified as byte-identical,
  ID-only drift, semantic drift, absent at claimed tag, or unreachable claimed
  host using direct local-tag and URL evidence.
- Historical schema versions are immutable. A local legacy file either becomes
  byte-identical to its claimed immutable source or is preserved under the exact
  version that actually introduced its bytes; semantic additions move to a new
  schema version instead of rewriting an old contract.
- The five `reconc.dev` schema identities are replaced for newly emitted
  artifacts by immutable, reachable, release-tagged locations. Historical
  artifacts are documented honestly; no mutable tag or unresolvable domain is
  treated as verified publication.
- Policy authoring receives a new schema version before `actions` is added.
  Legacy policy-config v1 remains available with its exact historical semantics;
  current MCP/custom-runtime/cache fields and future action fields are never
  retroactively attributed to the old v0.9.1 bytes.
- Policy-lock v2, v3, and v4 registration points to tags that actually contain
  the exact files. Known legacy URLs remain accepted only under explicit
  compatibility aliases with exact migration tests and no false publication
  claim.
- The current policy-lock v4 local file remains byte-identical to
  `reconc-v0.9.4`; repairing other contracts cannot mutate that frozen file.
- `internal/schema` derives default and enterprise-mirror locations from the
  artifact registry rather than one blanket v1 base URL and a hardcoded v4
  exception.
- Generated artifacts stamp the registered current URL, and each local schema's
  `$id` equals that exact URL. Runtime validation, offline verification, docs,
  examples, release assets, and SBOM/provenance inventory use the same registry.
- RFC 0001 reports one exact current schema and format version in every section;
  its required-fields table, migration chain, and runtime loader language match
  source and tests. The separate source-loader precedence discrepancy remains
  explicitly owned by TASK 154 rather than being silently redefined here.
- The exact next release version/tag used for any new immutable URL is selected
  only after Christopher explicitly authorizes that exact version. The task
  never reuses `reconc-v0.9.5`, invents a version, or stamps a future URL that
  does not yet have a publication plan. Christopher explicitly selected
  `v0.9.6` and `reconc-v0.9.6` on 2026-08-10.
- Build-time tests compare every frozen local schema with the corresponding Git
  tag object byte-for-byte and validate representative artifacts against every
  current schema.
- Release-trust verifies that every default schema URL resolves after
  publication and returns the exact registered bytes and digest. Missing files,
  redirects to mutable content, wrong IDs, tag drift, semantic mutation, extra
  unregistered schemas, and unreachable current URLs fail for exact reasons.
- Offline runtime behavior never requires network access. Online URL checks are
  restricted to release/publication verification.
- Documentation and release notes distinguish repaired current contracts from
  immutable historical artifacts without claiming that an old tag was changed.

## Sub-Tasks

- [x] Inventory all 22 local schemas, every `$id`, runtime URL constant, emitted
      `$schema`, schema resolver path, RFC reference, release asset, and validator
- [x] Compare each local schema byte-for-byte with every tag claimed by its URL
      and record absent, ID-only, and semantic differences
- [x] Verify current URL reachability and returned bytes for every non-tagged or
      externally hosted schema identity
- [x] Trace the introduction commit and first containing release tag for every
      schema absent from its currently claimed tag
- [x] Define the typed per-artifact schema registry and remove blanket-version
      assumptions from schema resolution
- [x] Define the compatibility policy for historical aliases, unpinned legacy
      URLs, missing-tag URLs, and enterprise mirror paths
- [x] Restore legacy local files to exact immutable bytes or move later semantics
      into newly versioned schema paths without deleting migration support
- [x] Create a new policy-config schema version for all post-v0.9.1 semantics;
      reserve Action Plane additions for TASK 154 and later owning tasks
- [x] Rebind policy-lock v2 and v3 to truthful immutable identities while keeping
      explicit legacy aliases and byte-equivalent migration coverage
- [x] Preserve policy-lock v4 bytes and its v0.9.4 identity exactly
- [x] Update every generated artifact and decoder to use the registry's current
      schema identity and reject unsupported future versions
- [x] Fix RFC 0001's format-3/format-4 contradiction and verify every schema,
      migration, and runtime-loader statement against source; record the
      source-order discrepancy as TASK 154 ownership
- [x] Update RFC index, architecture, documentation, commands, examples,
      manpage, guides, schema inventory language, and release documentation
- [x] Add build-time tag-object byte comparisons for all frozen schemas
- [x] Add schema-validation fixtures for every current artifact and every known
      legacy migration input
- [x] Add negative tests for semantic mutation, `$id` mismatch, absent tag file,
      wrong introduction tag, unregistered file, duplicate registry owner,
      mutable URL, bad enterprise path, and unsupported future version
- [x] Extend release-trust to fetch every newly published default URL and compare
      exact bytes and SHA-256 after the tag exists
- [x] Add a temporary-repository release fixture proving pre-tag preparation,
      tag creation, post-tag URL verification, and immutable re-verification
      without changing the real repository or remote
- [x] Verify publication inventory, harness pack, SBOM, provenance, checksums,
      source archive, and release assets include the exact registered schemas
- [x] Re-read every modified file and run schema, migration, compiler, runtime,
      release-trust, publication, full module, race, vet, static-analysis, and
      whole-module coverage measurement as review evidence

## Notes

This task runs after TASK 153 contract planning and before TASK 154 compiler
implementation. TASK 154 depends on its per-artifact registry and truthful new
policy-config version.

The complete 2026-08-10 inventory has 22 local schemas and no unclassified
file. Eleven v1 files have ID-only drift caused by replacing their historical
`main` IDs with nonexistent v0.9.1 bytes: completion report, global diagnostic,
global lifecycle, harness-pack manifest, installation receipt, policy fix plan,
policy lock v1, policy report, proof bundle, release manifest, and repository
install. Three v1 files have semantic plus ID drift: policy config,
repository-sync plan, and repository-sync report. Policy lock v2 also has
semantic plus ID drift. The five custom-runtime and neutral-hook v1 files use
unreachable `reconc.dev` IDs. Policy lock v3 is absent from its claimed v0.9.1
tag. Policy lock v4 is the sole byte-exact claimed contract.

Direct URL retrieval proved every raw-GitHub mismatch and the v3 404. The five
`reconc.dev` requests failed before an HTTP response. Local-byte introduction
truth is: the eleven ID-only v1 files and both repository-sync semantic files
are exact at v0.9.2; the five custom-runtime files, policy lock v2, and policy
lock v3 are exact at v0.9.3; policy config is exact at v0.9.4; policy lock v4 is
exact at v0.9.4. These locators identify bytes only and do not repair their
embedded IDs.

Runtime ownership is split today: `internal/schema` owns fourteen artifact
names and a blanket v1 base plus a v4 lock exception; `internal/customruntime`
owns five separate `reconc.dev` constants. Compiler, runtime, completiongate,
proofbundle, usercli, harnesspack, bootstrap, and customruntime stamp or validate
those identities. `scripts/release/copied-assets.tsv` manually enumerates all
22 release schema copies. The final registry must replace all three independent
enumerations rather than add a fourth.

Implementation now has a typed 26-contract registry plus a detached 22-file
forensic observation inventory, with exact schema and format versions, local
and release paths, current/legacy state, enterprise paths, introduction tags,
SHA-256 values, and registered aliases. Default and version-specific resolution
are registry-driven; format-1 through format-3 lock migration no longer contains
a second URL switch. The five former `reconc.dev` identities are input-only
aliases of the central owner. Build-time tests compare inherited bytes with
their exact historical tag objects and planned bytes in a disposable v0.9.6 tag.
The release copier and verifier consume the registry directly, while
`copied-assets.tsv` now owns non-schema assets only; the real release-trust
target proves that path end to end.

Compatibility is now pairwise rather than URL-only: a decoder must match one
registered schema identity and one format version owned by that same contract.
Aliases remain input-only, enterprise mirrors use the per-artifact registry
path, portable public proof bundles retain the public default identity, and
runtime validation stays offline. The current-lock portability regression now
tests the actual v4 contract instead of accidentally rechecking v3.

Focused registry, independent Draft 2020-12 validation, migration, publication,
bounded-I/O, and real release-trust gates are green. The final review also
proved canonical stable release-tag and safe asset naming, mechanically
recomputed every inherited classification, rejected duplicate JSON names and
unregistered local schema bytes before release inventory generation, rejected
unsafe or colliding release-copy paths, and fixed the legacy custom-runtime
response-budget boundary without weakening v1 compatibility.

The exact unchanged snapshot passed `make test`, including root and portable
template race suites plus the real release target; `make vet`, `make lint`,
`make coverage`, `make build`, `make self-host`, both module-integrity and tidy
checks, publication audit, harness-pack verification, ShellCheck, vulnerability
scanning, and diff/format checks are green. Whole-module statement coverage was
measured for both the root and portable-template modules as review evidence.
Christopher explicitly authorized source version `v0.9.6` and immutable tag
name `reconc-v0.9.6` on 2026-08-10. New contract identities may therefore be
prepared against that exact tag. The real tag must not be created halfway
through the Action Plane: TASK 165 proves the prepublication state and a real
temporary-repository tag flow; TASK 164 freezes and verifies the complete
`v0.9.6` source snapshot before any remote publication.

Verified hashes on 2026-08-10 include local policy-config v1
`52cc057e0c898b3a178d0d2dacee306cfd33c4b74c794161c7aa29ed60f1cf7e`
versus v0.9.1
`7e8f1d6c736338f4825d00a15ce35196c7d72b947e0a1e35038c811b9684113a`.
The v4 policy-lock file matches v0.9.4 at
`32f16bde36b7e8e5d0671c1e3f8bcbf35f810ad7699d93291d1ebb29831b3450`.
These are planning evidence and must be recomputed at task start.

No placeholder, mutable `main` URL, old tag, or guessed version may be stamped.
Historical contracts must be verified against existing real tag objects now;
new `reconc-v0.9.6` contracts must be verified locally and in the temporary tag
fixture now, then against the real immutable tag as TASK 164's final release
gate.

## Deviations

None.
