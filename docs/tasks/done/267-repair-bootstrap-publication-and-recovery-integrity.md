# TASK 267: Repair bootstrap publication and recovery integrity

## Why

Bootstrap publication has two current crash/error paths that can strand
managed artifacts. A crash after creating the deterministic
`.reconc-bootstrap-<digest>.tmp` file makes every retry of the same plan fail
with `os.ErrExist`. Separately, failure of the post-publication parent identity
check returns an empty `createdRecord` without removing the already linked or
copied target, so the caller cannot roll it back. Four bounded bootstrap JSON
loaders also validate with `Lstat` before `Open` but never prove that the opened
file is the path identity they inspected.

## Acceptance

- Applying the same plan after a simulated crash between stage creation and
  stage cleanup succeeds without deleting unrelated, changed, or user-owned
  files.
- Stale-stage handling runs only while the bootstrap transaction lock is held,
  recognizes only the exact managed stage naming and expected target context,
  and validates type, identity, ownership evidence, and bounded age or content
  before removal. An ambiguous residue fails closed with actionable recovery
  guidance.
- Every error after target publication either removes the exact created target
  and returns an empty record or returns the non-empty record plus cleanup error
  so the outer rollback can retry. Parent/file handles and stage files are
  closed and removed with `errors.Join` semantics.
- `LoadPlan`, install-receipt loading, `LoadRepositoryReceipt`, and
  `LoadSyncPlan` reject a path swapped between inspection and open. They retain
  size, strict-JSON, unknown-field, trailing-value, mode, and close-error
  behavior.
- Crash-injection tests cover stage residue, target publication followed by
  parent replacement, cleanup failure, hardlink and exclusive-copy paths, and
  user-created lookalike files on Darwin/Linux and portable Windows paths.
- Bootstrap and repository-sync tests, race tests, documentation, recovery
  guidance, and publication/release-trust gates pass.

## Sub-Tasks

- [x] Map the bootstrap lock, stage, publish, rollback, and recovery ownership boundaries
- [x] Implement exact stale-stage detection and locked recovery
- [x] Make every post-publication failure return rollback-capable state
- [x] Add stable opened-file identity checks to all four bootstrap JSON loaders
- [x] Remove `copyStagedExclusive` only after its test callers use the production rooted helper
- [x] Add adversarial crash, replacement, cleanup, and cross-platform tests
- [x] Update bootstrap recovery documentation and verification scripts where required
- [x] Run focused, race, publication, self-host, and complete repository gates

## Notes

- Current evidence: `internal/bootstrap/transaction.go` creates the stage with
  `O_CREATE|O_EXCL` at the deterministic name near `publishArtifactWithHooks`;
  no production residue sweep exists.
- Current evidence: the `validateCreatedParent` failure immediately after
  `record` construction closes the record and returns `createdRecord{}` without
  calling `removeCreatedRecord`. Later chmod/hash/cleanup failures already show
  the required record-preserving cleanup pattern.
- Current evidence: `internal/bootstrap/plan.go`, `receipt.go`,
  `repository_receipt.go`, and `repository_sync_plan.go` inspect with `Lstat`
  and then open without `os.SameFile` or an equivalent rooted identity proof.
- `Apply`, repository sync, candidate acceptance, removal, and receipt writes
  reach publication only below the canonical repository transaction lock.
  The unexported publication helper is therefore the locked recovery boundary;
  invalid plans return before publication, and direct calls are test-only.
- Re-hashing repository-sync before-images at build, journal-validation, and
  recovery boundaries is not part of this task. Those passes currently defend
  different trust boundaries and must not be removed as a speculative speedup.
- `appendUniqueDirectories` is bounded setup work. Optimize it only if a new
  benchmark demonstrates material cost after the correctness work.
- Existing `make self-host`, `make publication-audit`, release-trust, and
  Windows preflight entry points already own the required verification
  surfaces; no parallel bootstrap-only script was added.
- Release-trust exposed one pre-existing TASK 266 note that encoded measured
  coverage percentages in project text despite the repository's explicit
  review-only policy. The note now preserves the verification fact without a
  numeric pass/fail-shaped statement.
- Verification passed: focused and full `internal/bootstrap` tests; focused
  and full package race runs; Windows/amd64 bootstrap test cross-compilation;
  `make test-fast`; `make self-host`; `make publication-audit`; complete
  release-trust tamper and artifact tests; reference-doc drift, Vet,
  Staticcheck, formatting, TASK lifecycle, and diff checks.

## Deviations

None.
