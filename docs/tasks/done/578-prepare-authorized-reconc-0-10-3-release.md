# TASK 578: Prepare the authorized Reconc 0.10.3 release

## Why

Christopher explicitly requested product release `0.10.3` on 2026-10-19 for
the self-lockout fixes and operator administration of TASK 577. The verified
source needs committed release notes before the existing tag-bound
publication workflow can run, and README still declared `reconc-v0.9.8` as the
latest published release and installer source.

## Acceptance

- Release notes accurately describe the changes since `reconc-v0.10.2`,
  including the operator-only command surface and upgrade implications.
- README declares the actual latest published release `reconc-v0.10.2` for its
  installer sources; after publication a separate commit moves it to
  `reconc-v0.10.3`, because the publication audit requires the declared tag to
  exist.
- Production source, dependency pins, immutable schemas, release workflow and
  artifact matrix stay unchanged by this preparation.
- Reference checks, publication audit and diff checks pass; the preparation is
  archived, committed and pushed separately on `main`.
- Publication uses only the explicitly authorized `reconc-v0.10.3` identity;
  exact-candidate CI and CodeQL pass before tag creation and dispatch.

## Sub-Tasks

- [x] Verify authorization, source identity, existing release and publication contracts.
- [x] Write source-grounded release notes and correct the stale README release declaration.
- [x] Run focused preparation checks, reread changes, and archive the preparation.
- [x] Commit and push the prepared candidate for exact-commit hosted verification.

## Notes

- GitHub reports `reconc-v0.10.2` as the latest published release; no tag,
  release or release-note file exists for `reconc-v0.10.3`.
- After the preparation commit's CI and CodeQL pass, create an annotated tag at
  that exact clean remote commit, push only that tag, and dispatch
  `reconc-release.yml` with the tag as workflow ref and `tag` input; leave
  replacement and optional Windows smoke disabled.
- After publication, download the release inventory, verify checksums and
  provenance, then update the operator's installed CLI with `reconc update`
  as Christopher requested.
- TASK 577's control record is force-added with this commit like earlier
  archived TASKs.

## Deviations

None.
