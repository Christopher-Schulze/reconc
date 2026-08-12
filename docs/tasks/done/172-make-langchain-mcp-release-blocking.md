# TASK 172: Make LangChain MCP a prominent release-blocking feature

## Why

The Go-only MCP gateway and its proven LangChain interoperability are accurate
but buried below the README introduction. The real external-consumer test runs
in CI, yet the protected `main` ruleset does not require that check and the
release workflow can publish without executing it.

## Acceptance

- The README presents routed pre-execution MCP enforcement near the top with a
  truthful topology, concrete benefits, the explicit bypass boundary, and a
  direct link to the canonical LangChain configuration.
- Canonical documentation states that the pinned external LangChain proof is a
  mandatory release gate while preserving the exact supported boundary.
- The release workflow runs the hash-pinned official LangChain consumer against
  the exact release tag and cannot publish unless that job passes.
- Publication tests fail if README prominence, canonical examples, dependency
  pins, CI coverage, or the release-gate dependency drifts.
- GitHub protects `main` with the exact `LangChain MCP interoperability` status
  check in addition to the existing required checks.
- Relevant local tests pass, the live ruleset is re-read after update, and no
  unrelated repository content changes.

## Sub-Tasks

- [x] Design and add the prominent README feature surface
- [x] Add the exact-tag LangChain release gate
- [x] Strengthen documentation and publication contracts
- [x] Run focused and complete verification
- [x] Update and verify the live `main` ruleset
- [x] Reconcile TASK state and commit the completed change

## Deviations

None.
