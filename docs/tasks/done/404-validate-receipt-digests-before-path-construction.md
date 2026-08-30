# TASK 404: Validate receipt digests before path construction

## Why

Historical receipt inspection checks only that `plan_digest` is at least 12 bytes before interpolating it into a plan path. A malformed value can drive path cleaning outside `.reconc` before later validation rejects the pair.

## Acceptance

- Receipt `plan_digest` must be canonical SHA-256 before any filename or path is derived from it.
- Every derived historical receipt and plan path remains inside the validated repository control directory.
- Malformed candidates are skipped without reading outside the repository and without deleting either pair member.
- Adversarial tests cover traversal separators, drive prefixes, mixed-case hex, short values, long values, and valid historical pairs.

## Sub-Tasks

- [x] Validate digest shape at the earliest decode boundary.
- [x] Bind derived paths through the existing safe bootstrap target contract.
- [x] Add path-observation hooks proving no out-of-root read occurs.
- [x] Run focused receipt-retention tests.

## Notes

- Verified from finding 72 and its receipt-retention recurrence in finding 143.
- Later `plan.RepoRoot` and receipt validation limit impact but do not restore the violated path-containment invariant.
- Confirmed in current code: `inspectObsoleteBootstrapReceipt` accepted any digest of at least 12 bytes, then passed it to `installReceiptPath` and interpolated it into the plan path before `LoadPlan` validation.
- Receipt and plan reads now use paths independently bound to the repository `.reconc` directory; malformed digests are rejected before either digest-derived path helper runs.
- Deterministic read hooks prove malformed traversal, platform-prefix, case, and length inputs cannot trigger a plan read or cleanup, while valid historical pairs retain their exact paths.
- Verification passed: focused receipt-retention tests, `make test-fast`, `gofmt`, and `git diff --check`.

## Deviations
