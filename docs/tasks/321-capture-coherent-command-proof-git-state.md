# TASK 321: Capture coherent command-proof Git state

## Why

Command-proof capture reads HEAD and materializes the index tree as separate Git operations without a repository-wide Git snapshot. Store validation then releases its local proof lock before writing, leaving a mutation window.

## Acceptance

- HEAD and index-tree identity are proven to belong to one coherent stable capture.
- Proof publication remains conditional on that exact capture through the write boundary.
- Parallel Git mutation cannot create a pair that never coexisted or publish a newly stale success.
- Existing clean-worktree checks, retention, private storage, and load-time revalidation remain intact.

## Sub-Tasks

- [ ] Specify coherent Git capture without claiming control of external Git writers
- [ ] Add bounded before/after validation around capture and publication
- [ ] Add parallel HEAD/index mutation regressions
- [ ] Run commandproof, completion, proofbundle, and race gates

## Notes

- Evidence: `internal/commandproof/proof.go:140-188,254-285`.

## Deviations

None.
