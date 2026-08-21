# TASK 193: Extract inline policy blocks linearly and within bounds

## Why

Inline policy extraction first materializes every regex match and computes each
block's starting line by counting newlines from the beginning of the document.
Many blocks therefore cause repeated prefix scans and can create an unbounded
number of intermediate matches before the final source bundle limit is checked.

## Acceptance

- Extraction scans each bounded source once, maintains line numbers
  incrementally, and stops at an explicit per-source and aggregate block cap.
- Fenced-block syntax, CRLF/LF line numbers, block identifiers, trimming, and
  error locations remain compatible.
- Cap errors identify the source and limit without retaining all preceding
  block bodies unnecessarily.
- Tests cover zero blocks, maximum blocks, cap+1, malformed fences, CRLF, large
  prefix text, and deterministic ordering.
- Benchmarks demonstrate linear scaling by source bytes plus emitted blocks.

## Sub-Tasks

- [x] Specify inline block grammar and limits
- [x] Implement a single-pass extractor with incremental line tracking
- [x] Share the extractor with doctor/reference inspection where applicable
- [x] Add boundary, differential, and benchmark tests
- [x] Run ingest, doctor, and complete gates

## Notes

- `extractInlineBlocks` now scans bounded source bytes linearly, recognizes the
  exact opening/closing fence grammar, tracks LF/CRLF line numbers
  incrementally, and caps each source at 512 blocks before retaining another
  body. The legacy regex remains only as a compatibility test fixture.
- `loadEntryFileWithBlocks` propagates cap errors as typed source errors; inline
  source ordering, IDs, trimming, and CRLF behavior remain unchanged.
- Boundary/differential tests pass. Apple M1 benchmark on a 4,096-line prefix:
  linear extractor 52.713 us/op versus regex baseline 559.906 us/op.

## Deviations

None.
