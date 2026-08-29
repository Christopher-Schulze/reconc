# TASK 321: Capture coherent command-proof Git state

## Why

Command-proof capture reads HEAD and materializes the index tree as separate Git operations without a repository-wide Git snapshot. Store validation then releases its local proof lock before writing, leaving a mutation window.

## Acceptance

- HEAD and index-tree identity are proven to belong to one coherent stable capture.
- Proof publication remains conditional on that exact capture through the write boundary.
- Parallel Git mutation cannot create a pair that never coexisted or publish a newly stale success.
- Existing clean-worktree checks, retention, private storage, and load-time revalidation remain intact.

## Sub-Tasks

- [x] Specify coherent Git capture without claiming control of external Git writers
- [x] Add bounded before/after validation around capture and publication
- [x] Add parallel HEAD/index mutation regressions
- [x] Run commandproof, completion, proofbundle, and race gates

## Notes

- Evidence: `internal/commandproof/proof.go:66-201,259-291`.
- `StoreSuccess` now uses `CaptureCurrent` for a coherent two-sample HEAD/index capture before writing and confirms the same snapshot after the receipt is published.
- External Git writers are not locked out or claimed to be controlled; any observed drift rejects the new proof, while load-time current-candidate validation quarantines an already-written stale receipt from policy evidence.
- Existing staged-clean, current-snapshot, concurrent-capture, index-lock, tamper, expiry, completion, and proof-bundle regressions cover the retained contracts. Commandproof/completion/proofbundle/CLI race suites, `make test-fast`, `make vet`, Staticcheck v0.8.1, and `make self-host` passed.

## Deviations

None.
