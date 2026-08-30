# TASK 466: Prepare Reconc v0.9.8 release

## Why

The fully verified post-v0.9.7 hardening queue is ready for a new stable
release. The release candidate must advance the product identity without
rewriting any immutable schema publication identity, publish accurate upgrade
and release documentation, and remain bound to the repository's protected
tag-only release workflow.

## Acceptance

- Candidate-owned source, build, release-installer examples, current
  interoperability proof, migration guidance, and release notes consistently
  identify product version `0.9.8`; latest-stable bootstrap remains pinned to
  the actually published v0.9.7 tag until publication completes.
- Unchanged public JSON contracts retain their exact immutable introduction
  tags and bytes; the publication audit explicitly permits a schema
  introduction tag to precede the product release version but never follow it.
- Release notes accurately summarize the material security, data-integrity,
  correctness, performance, runtime, and portability changes since v0.9.7 and
  state the compatibility boundary.
- Focused version, schema-publication, documentation, and release-publication
  checks pass; formatting, Vet, Staticcheck, publication audit, and Git diff
  checks pass without running duplicate local race or platform suites.
- The candidate is archived and committed once on `main`; remote CI and CodeQL
  must pass on that exact commit before the protected `reconc-v0.9.8` tag is
  created or the manual release workflow is dispatched.

## Sub-Tasks

- [x] Reconcile product-version and immutable-schema release semantics
- [x] Update version owners, public documentation, and contract assertions
- [x] Write and review the v0.9.8 release notes
- [x] Run focused local candidate verification
- [x] Archive and commit the release candidate

## Notes

- Live preflight at `73d6f16f` found no local or remote `reconc-v0.9.8` tag and
  no GitHub release with that identity. CI run `33329584645` and CodeQL run
  `33329584640` both passed on the exact clean `main` commit.
- Format 6 and every other public schema retain their already published bytes
  and registry introduction tags. A product-only patch release must not mint a
  duplicate schema URL or rewrite lockfiles solely to match the binary version.
- The exact-tag release workflow owns native Windows, LangChain, race,
  release-trust, self-host, artifact, checksum, SBOM, and provenance gates.
  Local verification stays focused to avoid duplicating those expensive gates.
- Focused version, immutable-schema, action-plane, LangChain-contract, installer,
  and release-publication tests passed. `make test-fast`, `make vet`, `make
  lint`, `make publication-audit`, and `git diff --check` passed from the final
  candidate. Race, Windows, release-trust, self-host, artifact, and provenance
  gates remain assigned to the exact-tag GitHub workflows.

## Deviations

None.
