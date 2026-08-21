# TASK 227: Unify inline policy-block discovery

## Why

Policy ingestion and deep doctor inspection recognize fenced `reconc` blocks
with separate regular expressions and extraction functions. Ingestion accepts
horizontal whitespace after the closing fence, while doctor does not. A source
can therefore compile successfully but disappear from doctor template and
preset-reference inspection. Syntax ownership must be singular so diagnostic
truth cannot drift from compilation truth.

## Acceptance

- Ingest and doctor consume one authoritative fenced-block scanner or one
  shared immutable match representation; no second syntax regex remains.
- Opening-fence whitespace, closing-fence whitespace, LF, CRLF, multiple
  blocks, empty content, unterminated fences, line anchoring, and surrounding
  Markdown produce identical inclusion decisions on both paths.
- Ingest preserves exact block order, source path, line start, block ID,
  trimmed-content behavior, maximum block count, aggregate byte limits, and
  collision-resistant identity behavior.
- Doctor preserves its own total-source byte budget and diagnostic labels while
  inspecting exactly the blocks that ingestion would compile.
- Malformed or excessive input retains the current bounded rejection or
  non-match behavior at the owning boundary; untrusted Markdown cannot cause
  quadratic scanning or unbounded materialization.
- A differential corpus invokes both consumers and proves parity for valid,
  malformed, boundary-sized, CRLF, whitespace, and multi-block documents.
- Existing inline-source, doctor deep-check, preset-reference, fuzz, and
  portable-harness tests remain green.

## Sub-Tasks

- [x] Specify the canonical fenced-block grammar and extraction metadata
- [x] Extract one bounded scanner shared by ingest and doctor
- [x] Preserve ingest identities, line numbers, order, and cardinality limits
- [x] Add differential grammar, malformed-input, and boundary regressions
- [x] Update policy-source documentation and run ingest/doctor gates

## Notes

- Session finding: `#29`.
- Primary code: `internal/ingest/source_loader.go` and
  `internal/cli/doctor_deep.go`.
- TASK 193 established bounded linear extraction and identity behavior. This
  TASK must reuse that contract rather than replace it with a general-purpose
  Markdown parser or a second approximation.
- CommonMark completeness is not required. The existing intentionally narrow
  Reconc fence grammar remains the product contract.
- `ingest.ScanInlinePolicyBlocks` now owns recognition and produces the exact
  `PolicySource` records consumed by compilation; deep doctor consumes those
  records directly and retains its independent 64 MiB aggregate read budget.
- Differential coverage includes plain Markdown, LF, CRLF, horizontal fence
  whitespace, multiple and empty blocks, unterminated and indented fences, the
  512-block boundary, and rejection at 513 blocks.
- Verification: `go test ./internal/ingest ./internal/cli`, all doctor-focused
  tests, focused race runs, and `go vet ./internal/ingest ./internal/cli` pass.
  The first CLI run lacked Homebrew in the tool PATH and correctly reported
  unavailable Bun-backed adapters; the complete CLI package passes with the
  installed Bun directory restored to PATH.

## Deviations

None.
