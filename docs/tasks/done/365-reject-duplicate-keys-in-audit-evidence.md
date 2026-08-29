# TASK 365: Reject duplicate keys in audit evidence

## Why

Strict audit decoding rejects unknown fields but accepts duplicate known JSON keys. Verification hashes the decoded last-key-wins structure while export preserves the original ambiguous bytes, allowing consumers with different duplicate-key semantics to disagree.

## Acceptance

- Audit JSON rejects duplicate object keys at every nesting level before semantic verification.
- Verification, digest calculation, and export operate on one unambiguous record meaning.
- Existing valid archives remain byte-preserving and compatible.
- Tests cover top-level and nested duplicate keys with conflicting values.

## Sub-Tasks

- [x] Add duplicate-key detection to strict audit decoding.
- [x] Apply it consistently to verification and export paths.
- [x] Add ambiguous-record and valid-archive regressions.
- [x] Run focused audit and JSONL tests.

## Notes

- Verified from OMP session `01a04cc5-6a01-7526-95bb-b2715553daf3`, finding #111.
- Current evidence: `internal/audit` uses `encoding/json` strict-field decoding, which does not reject duplicate known keys, and exports verified raw bytes.
- `decodeStrictJSON` now token-walks every object and array before semantic decoding, rejecting decoded-key collisions including escaped-equivalent names. `Verify`, `Tail`, `Stats`, append checkpoints, detached-head reads, and `ExportJSONL` all use this single strict path.
- Regression coverage rejects conflicting top-level, nested, array-contained, and escaped-equivalent keys; a mutated audit record fails both verification and export before any bytes are written. A rotated valid ring exports byte-identically. `go test ./internal/audit ./internal/jsonl -count=1 -timeout=120s` passed.

## Deviations

- The repository-wide race, release-trust, publication, and other heavy suites were not run, per the explicit execution constraint. Windows-specific tests were not run locally; cross-platform source compatibility remains covered by CI.
