# TASK 435: Harden bootstrap stage recovery

## Why

Repository-sync journal removal is not followed by parent-directory durability, stale-stage cleanup revalidates a leaf before a later name-based remove, and `WriteNew` can leave its temporary hardlink name after the target is published.

## Acceptance

- Journal deletion is durably synchronized before a transaction is reported complete.
- Stage removal is bound to the exact verified leaf identity through deletion.
- Every post-link error path attempts safe temp cleanup; crash residue has an identity-validated recovery path.
- Deterministic crash/race tests cover journal resurrection, leaf replacement, hardlinks, post-link sync failure, and stale temp recovery.

## Sub-Tasks

- [x] Define durable terminal states for journal, stage, target, and temporary links.
- [x] Carry verified parent/leaf identity into removal and cleanup.
- [x] Add failure injection at every link, sync, close, remove, and directory-sync boundary.
- [x] Run focused bootstrap and atomicfile tests.

## Notes

- Verified from findings 137, 138, and 140.
- Finding 141 is already covered by TASK 405 for the separate removal/restore path.
- Journal cleanup now validates the exact journal bytes and identity, rejects disappearance or replacement, checks resurrection, and synchronizes the bound parent.
- Bootstrap stage and `WriteNew` residue cleanup revalidate exact identities immediately before rooted removal; recovery accepts only canonical temporary hardlinks to the exact target.
- Focused tests passed: `go test ./internal/atomicfile -count=1`; `go test ./internal/bootstrap -run 'Test(PublishArtifact|RepositorySync|BootstrapArtifactDurability)' -count=1`; final boundary subsets passed after review.

## Deviations
