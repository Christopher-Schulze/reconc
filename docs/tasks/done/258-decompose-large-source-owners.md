# TASK 258: Decompose large source owners

## Why

Four production files each own between roughly 1,300 and 2,200 lines and mix
multiple already-visible responsibilities. This increases review surface,
merge conflict probability, and agent re-entry cost. The change must be a pure
ownership decomposition, not a speculative redesign or exported-API rewrite.

## Acceptance

- `internal/runtime/evaluator.go` is split along existing evaluation,
  normalization, command-matching, batching, and violation-rendering seams.
- `internal/runtime/agentsession/stop_cache.go` is split along fingerprint,
  Git snapshot, completion snapshot, cache storage, and policy-check seams.
- `internal/jsonl/jsonl.go` is split along decoding, validation, publication,
  recovery, and indexed-read seams.
- `internal/cli/hook_cmd.go` is split along lifecycle management, runtime
  dispatch, response adaptation, timing, and claim-command seams.
- Package names, symbols, exported signatures, error text, build tags,
  deterministic ordering, privacy bounds, and platform behavior remain
  unchanged unless a regression test proves an existing defect.
- Every moved declaration has exactly one owner, no compatibility wrapper or
  duplicate implementation remains, and package-local tests plus the complete
  race gate pass.
- Architecture documentation reflects the resulting ownership boundaries
  without maintaining a brittle file inventory.

## Sub-Tasks

- [x] Map declarations, callers, build constraints, and ownership seams
- [x] Decompose the runtime evaluator without behavior drift
- [x] Decompose agent-session Stop caching without behavior drift
- [x] Decompose JSONL processing without behavior drift
- [x] Decompose hook CLI/runtime handling without behavior drift
- [x] Update architecture documentation and verify package ownership
- [x] Run focused, race, and repository-wide verification
- [x] Archive the completed TASK and commit the verified change

## Notes

- Current owner sizes are 2,187 lines for `runtime/evaluator.go`, 2,107 lines
  for `agentsession/stop_cache.go`, 1,543 lines for `jsonl/jsonl.go`, and 1,380
  lines for `cli/hook_cmd.go`.
- Existing unexported seams and package-local tests are the design authority.
  No new dependency or package boundary is required.
- The split copied one contiguous declaration range at a time after proving the
  exact source line counts. Source SHA-256 identities were unchanged until all
  replacement owners compiled without the originals; the old owners were then
  removed without leaving wrappers or duplicate declarations.
- The largest resulting owner is 694 lines. The affected-package race gate,
  full CLI race test, focused metadata-surface parity tests, focused `go vet`,
  repository-wide `make test-fast`, formatting, and `git diff --check` pass.
- CLI surface parity initially caught that `hook_cmd_core.go` did not match the
  established `*_cmd.go` source-discovery convention. Renaming the owner to
  `hook_core_cmd.go` restored the canonical contract without weakening the
  test or changing command behavior.

## Deviations

None.
