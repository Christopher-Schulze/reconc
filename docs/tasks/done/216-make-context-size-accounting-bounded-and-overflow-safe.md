# TASK 216: Make context-size accounting bounded and overflow-safe

## Why

Context-size scanning accepts an unbounded file slice and converts `int64` file
sizes into native `int` token estimates. On 32-bit targets or sufficiently
large/sparse files, per-file and total token counts can overflow and make an
over-budget context appear acceptable. Repeated duplicate inputs are normalized
before deduplication.

## Acceptance

- File input count and path length are bounded before allocation and
  normalization; duplicates do not consume repeated filesystem work.
- Per-file tokens, total tokens, and token budget use an overflow-safe integer
  domain with checked/saturating addition and a stable JSON contract.
- Files too large to represent produce an explicit over-budget/error result,
  never a wrapped pass.
- File metadata is captured from a stable contained identity without exposing
  an external resolved path in diagnostics.
- Tests cover 32-bit arithmetic, sparse huge files, sum overflow, duplicate
  floods, symlink swaps, and limit boundaries.

## Sub-Tasks

- [x] Define public numeric and input cardinality limits
- [x] Implement overflow-safe token accounting
- [x] Bound and deduplicate requested paths early
- [x] Stabilize contained metadata inspection
- [x] Add architecture, sparse-file, race, and boundary tests
- [x] Run context-size, CLI, platform, and complete Go gates

## Notes

- `Scan` rejects more than 4,096 requested paths and path strings above 1 KiB
  before allocating the deduplication map. Normalized duplicates are skipped
  before filesystem inspection. The public JSON numeric fields now use signed
  64-bit values, so 32-bit hosts cannot truncate token counts.
- Per-file estimates and totals use checked saturating addition. A sparse
  1 TiB file remains representable, is reported with its stable size, and
  trips a small budget instead of wrapping into a passing negative count.
  Repository files are inspected through `boundedio.WithRegularFileSnapshot`:
  final symlinks and special files fail closed, opened identity and metadata
  are revalidated, and only contained non-symlink regular files are reported.
- Duplicate floods, maximum path/count boundaries, sparse files, arithmetic
  overflow, and existing containment tests pass. The complete CLI/platform
  and repository gates remain for queue completion.

## Deviations

None.
