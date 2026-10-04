# TASK 576: Prepare the authorized Reconc 0.10.2 release

## Why

Christopher explicitly selected product release `0.10.2`. The verified source
at `32248652e7059979b419fb1709a4ad38c0d63150` needs committed release notes
before the existing tag-bound publication workflow can run. The documentation
also incorrectly names an older release as the latest published version.

## Acceptance

- Release notes accurately describe the ten commits since `reconc-v0.10.1`,
  including the stricter evidence contracts and upgrade implications.
- Current documentation links to the actual latest published release instead
  of pinning a stale or not-yet-published product version.
- Production source, dependency pins, immutable schemas, release workflow,
  and artifact matrix remain unchanged.
- Reference checks, publication audit, and diff checks pass; this preparation
  is archived, committed, and pushed separately on `main`.
- Publication uses only the explicitly authorized `reconc-v0.10.2` identity.
  Exact-candidate CI and CodeQL must pass before tag creation and dispatch.

## Sub-Tasks

- [x] Verify authorization, source identity, existing release, and publication contracts.
- [x] Write source-grounded release notes and correct the stale documentation pointer.
- [x] Run focused preparation checks, reread changes, and archive the preparation.
- [x] Commit and push the prepared candidate for exact-commit hosted verification.

## Notes

- Current remote `main` and local HEAD both identify `32248652`; its CI and
  CodeQL are successful. Root/template races, vet, Staticcheck, release trust,
  and isolated self-hosting already passed for this production source.
- GitHub reports `reconc-v0.10.1` as the latest published release. No local
  release-note file, remote tag, or GitHub release exists for `reconc-v0.10.2`.
- The only preparation edits are this control record, the task board, new
  `.github/releases/reconc-v0.10.2.md`, and the Release State documentation
  paragraph. No version literal is introduced into production or build defaults.
- After the preparation commit's CI and CodeQL pass, create an annotated tag
  at that exact clean remote commit and push only that tag. Dispatch
  `reconc-release.yml` with the tag as both workflow ref and `tag` input;
  leave replacement and optional Windows smoke disabled.
- The existing release workflow owns immutable-schema publication, real
  LangChain interoperability, source/static/race/trust/self-host gates,
  cross-platform builds, canonical artifact verification, provenance, and
  draft-to-published inventory reconciliation. These gates are not weakened.
- After publication, independently download the complete release inventory,
  verify canonical artifacts and checksums, validate tag/commit-bound GitHub
  provenance, and smoke only the native version/help surface. Do not change
  the installed user CLI or publish repository-targeted Reconc state.
- Local preparation verification passed: generated reference checks,
  current-tree/history publication audit, embedded harness-pack verification,
  and `git diff --check`. Release-note claims were checked against the actual
  production diff since `reconc-v0.10.1`. No production or schema bytes changed.

## Deviations

None.
