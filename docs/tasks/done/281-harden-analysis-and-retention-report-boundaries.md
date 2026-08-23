# TASK 281: Harden analysis and retention report boundaries

## Why

Impact Lab manifest/corpus file loaders `Lstat` a non-symlink regular file and
then use a path-based reader without proving the opened identity is the file
that was inspected. Retention dry-run records an inspection error but continues
to publish derived class fields from the zero-value result. Several bounded
analysis helpers also perform avoidable whole-buffer string conversion or
quadratic token scans. These are correctness and predictable-cost gaps in
read-only reporting paths.

## Acceptance

- Impact delta-manifest and corpus file reads use one stable non-symlink regular
  file snapshot with open/path identity revalidation, size bounds, mutation
  detection, and close-error propagation.
- Retention dry-run leaves a class explicitly incomplete/unknown after
  `jsonl.Inspect` failure. It never derives bytes-freed, files-removed, or
  compliance-looking values from a zero-value result.
- Report schemas distinguish a real zero from unavailable data without breaking
  old decoders; any schema/format change has migration and generated-reference
  updates.
- Source-hygiene marker detection is linear in line/marker bytes and preserves
  case folding, token-boundary semantics, findings, exemptions, and order.
- Byte-buffer searches use `bytes.Contains` or equivalent without full string
  copies where the input is already validated bytes.
- Redaction counts continue to mean redacted occurrences, not post-dedup output
  cardinality. No "fix" changes that metric without a documented contract
  decision.
- Tests cover symlink swaps, in-place mutation, inspect failure, legitimate
  zero results, maximum reports, marker overlap, and large byte buffers.
- Impact/retention/assurance/adopt docs, schemas, generated references, scripts,
  and complete gates pass.

## Sub-Tasks

- [x] Replace Impact Lab path reads with stable regular-file snapshots
- [x] Model retention inspection failure separately from a successful zero result
- [x] Propagate any report-contract change through schemas and generated references
- [x] Linearize source-hygiene marker matching
- [x] Remove proven whole-buffer string copies in analysis helpers
- [x] Add replacement, mutation, failure, zero-result, and maximum-input tests
- [x] Update analysis/retention documentation and verification scripts
- [x] Run impact, retention, assurance, schema, race, and complete gates

## Notes

- Current evidence: `DecodeDeltaManifestFile` and `DecodeCorpusFile` call
  `os.Lstat`, then `boundedio.ReadFile`; unlike `ReadRegularFile`, that sequence
  does not bind the opened file to the inspected path identity.
- Current evidence: `retention.enforceJSONL` appends an error from
  `jsonl.Inspect` but continues to copy fields from the zero-value result.
- Current evidence: `adopt.contains` converts a byte slice to string before
  `strings.Contains`; source-hygiene marker scanning repeatedly checks prefixes.
- The proposed Impact Lab optimization that skips the second content hash when
  inode/size/mtime are unchanged is rejected. It would miss content mutation
  followed by metadata restoration and weaken the comparison trust boundary.
- The CI finding truncation counter is currently correct when an error replaces
  one retained non-error: exactly one other finding becomes omitted. Do not
  change it based on the reviewed claim.
- `DecodeCorpusFile` and `DecodeDeltaManifestFile` retain their established
  symlink diagnostic while acceptance now depends on
  `ReadRegularFileSnapshot`, including post-read identity and metadata checks.
- Run-decision class JSON adds optional `inspection_status`. `complete`
  distinguishes a measured zero; `unknown` suppresses derived text metrics
  after an inspection failure. Legacy decoders ignore the additive field and
  new decoders treat its absence as legacy/unspecified.
- Source sentinel matching now tracks quote/comment state in one forward pass.
  The 1 MiB benchmark measured 0 allocations and about 240 MB/s on the audit
  machine. Adoption's 1 MiB byte search also measured 0 allocations.
- Existing redaction-count semantics and tests were left unchanged. No owned
  schema or generated registry encodes the internal retention report shape;
  `reference-docs-check` confirmed no generated-reference drift.
- Verification passed: focused and complete package tests, focused Race,
  Windows/Linux cross-compilation, reference-docs, publication/harness audits,
  Vet, Staticcheck, and self-hosting.

## Deviations

None.
