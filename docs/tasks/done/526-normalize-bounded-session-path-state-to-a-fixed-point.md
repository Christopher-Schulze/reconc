# TASK 526: Normalize bounded session path state to a fixed point

## Why

Session normalization deduplicates and bounds `WritePaths`, then rebuilds
`WriteEpochs` from the original unbounded write-path list. When path count or
byte limits drop one or more writes, epochs for those discarded paths can
survive. The resulting state violates the normalizer's own invariant that every
epoch key must refer to a retained write path, so a second normalization can
change state again and the trusted fast path rejects the first result.

This task implements audit candidate C226. The fix must preserve the evidence
overflow record and all causal write-epoch semantics while making normalization
canonical and idempotent for valid, legacy, malformed, and over-limit state.

## Acceptance

- Rebuild `WriteEpochs` only from the exact canonical `WritePaths` retained
  after sorting, exact deduplication, per-item bounds, count bounds, and aggregate
  byte bounds.
- Guarantee `keys(WriteEpochs)` is a subset of `WritePaths`, every retained
  epoch is non-zero, and no discarded or duplicate spelling survives through
  the epoch map.
- Preserve the maximum epoch associated with a retained exact path according to
  the current mutation semantics; do not merge lexically different path
  identities or invent epochs for paths without evidence.
- Make normalization a fixed point: normalizing any admitted input twice must
  produce deeply equal state and byte-identical deterministic JSON.
- Keep `sessionStateIsNormalized(normalizeSessionState(state)) == true` for all
  inputs that can be represented within the session-state contract.
- Preserve existing bounds and semantics for reads, writes, commands, claims,
  command results, pending tool calls, retired keys, consumed approvals,
  evidence generations, and Antigravity high-water state.
- Preserve and deterministically combine the pre-existing overflow flag/reason/
  limit with any new overflow found during normalization. The fix must not clear
  earlier evidence that state exceeded a bound.
- Avoid map/slice aliasing that lets a caller mutate the normalized state through
  retained input collections.
- Keep the trusted mutation fast path allocation behavior for already normalized
  states; do not rebuild every collection when the existing fixed-point check
  succeeds.
- Add table-driven tests for exactly-at-limit, count overflow, byte overflow,
  duplicate paths, missing epochs, zero epochs, extra epoch keys, sorted and
  unsorted inputs, nil collections, and combined overflow reasons.
- Add property-style deterministic coverage over varied path counts and epoch
  maps proving subset, bounds, fixed-point, stable encoding, and input isolation.
- Pass session-state tests under the race detector, the existing allocation
  checks, and every required repository gate.

## Sub-Tasks

- [x] Read the complete normalization/admission code, all state mutators and serialization paths, evidence overflow combination, legacy loading, and fast-path allocation tests.
- [x] Write the full state invariants and choose one retained-write iteration that cannot reintroduce discarded epoch keys.
- [x] Apply the surgical normalizer fix while preserving exact-path identity, causal epochs, collection ownership, and overflow reporting.
- [x] Add limit, malformed-state, idempotence, deterministic-encoding, aliasing, and property-style regressions.
- [x] Measure already-normalized and over-limit normalization allocations; remove only regressions introduced by the correction.
- [x] Check every session adapter and persistence caller for assumptions about extra epoch keys or normalization side effects.
- [x] Update internal architecture documentation only if the externally described session-state invariant changes or was incomplete.
- [x] Run focused race/allocation tests and all required repository gates; re-read every changed file before archival.

## Notes

### Observed behavior

- Epoch rebuild iterated the unbounded sorted write list, so keys dropped by
  count, byte, or item-size limits survived in `WriteEpochs`.
- `sessionStateIsNormalized` then rejected that result, so a second
  normalization could still change state and the trusted mutator fast path
  never stuck.

### Implementation

- Rebuild `WriteEpochs` only from retained `state.WritePaths` after bounding.
- Evidence merger ignores epoch keys whose path was not retained as a write.
- The existing collection-semantics test encoded the old extra-key behavior
  for an oversize path; it now expects the subset invariant.

### Verification

- Table, count-limit, byte-budget, aliasing, JSON stability, and property
  tests plus `TestEvidenceMergerIgnoresEpochsWithoutWritePaths`.
- Focused `go test -race` on agentsession passed. Canonical admission still
  early-returns; over-limit maps are smaller because discarded keys are gone.
  `make lint` and `make test` (uncached race plus release-trust) passed.

## Deviations

None.
