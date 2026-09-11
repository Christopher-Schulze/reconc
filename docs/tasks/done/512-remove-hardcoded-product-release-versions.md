# TASK 512: Remove hardcoded product release versions

## Why

Hardcoded product versions in source, tests, build defaults, and documentation
force unrelated development changes and predeclare a future release.
Christopher requires version-free development: only his explicitly selected
Git tag assigns a product version to a chosen commit. The immediate CI failure
from a stale LangChain version assertion exposed this coupling.

## Acceptance

- AGENTS.md forbids autonomous product-version assignment, bumps, tags, and
  publication; only Christopher can authorize a versioned commit.
- Direct and managed development builds identify development/source state;
  release builds derive their product version from the exact selected Git tag.
- CI, interoperability, artifact verification, and documentation no longer
  depend on a current or future product-version literal.
- New schema identities are independent of product release numbers while
  published compatibility identities and offline validation remain sound.
- Real behavior tests cover development and explicit-tag identity, rejection
  of mismatched/dirty release sources, and existing integration boundaries.
- Relevant validation passes, the task is archived, committed, and pushed.

## Sub-Tasks

- [x] Record release authority in AGENTS.md and inventory version coupling.
- [x] Implement development and explicit-tag build identities and their callers.
- [x] Decouple unpublished schema identities and verification from release numbers.
- [x] Propagate version-free current documentation and regression contracts.
- [x] Run required gates, re-read the diff, archive, commit, and push.

## Notes

- CI run 34579305219, job 103198909436 fails with `Reconc binary version
  drifted` before exercising MCP. With its expectation corrected, the real
  pinned LangChain workflow passed locally, proving the original version gate
  was the immediate failure. The subsequent explicit user instruction requires
  removing the underlying product-version coupling, not updating its literals.
- No product tag, release publication, or version assignment is authorized.
- Preserve external dependency pins, format/protocol revisions, and immutable
  historical release/schema identities as separate compatibility facts.
- Use one build-identity resolver for managed builds and CI, with runtime VCS
  metadata for direct builds. Explicit-tag resolution must bind clean HEAD.
- Content-addressed identities for changed schemas avoid reserving a future
  product tag. Release verification must bind their files to its explicit tag
  without changing the runtime's offline trust boundary.
- Managed build and direct CLI smoke passed without a product version;
  schema, build-provenance, schema-asset, publication-contract, and generated
  reference tests passed. Development SBOM generation and verification pass.
- `make test` passes, including publication audit, uncached root and portable
  template race suites, and the complete isolated release-trust workflow.
  The real tagged fixture build completed in 95 seconds. `make vet`,
  `make lint`, `make self-host`, the pinned LangChain integration, and
  `git diff --check` pass. The product repository's tag references are unchanged.
- Self-hosting exposed the embedded harness pack's product-range coupling.
  Bundled archives now retain complete manifest, inventory, size, and hash
  verification independently of the build label; external archive loading
  retains the declared product range. Self-hosting and focused corruption,
  identity, and historical-plan binding tests pass.
- Existing Windows integration failures at the prior commit are tracked in
  TASK 513. Native Windows execution is separate from local macOS proof.

## Deviations

None.
