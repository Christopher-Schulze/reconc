# TASK 256: Reduce verification friction and refresh toolchain trust

## Why

Fresh repository-wide audit evidence shows that candidate-branch Windows CI
spends about seventeen minutes rerunning the complete native suite after its
focused runtime preflight has already passed, while the same full native suite
is independently required by the exact-tag Release workflow. Local all-package
verification also has no explicit low-memory fast path, and one command-proof
deadline test couples a 150 ms lock contract to an unrelated real Git startup,
which became flaky under whole-repository package pressure. Several direct Go,
analysis-tool, and GitHub-owned action pins also have newer stable releases.

## Acceptance

- `make test-fast` provides a documented cached root/template test loop with a
  positive configurable package-parallelism limit that defaults safely for an
  8 GiB development machine. `make test` retains uncached race, publication,
  and release-trust authority.
- Candidate pushes and pull requests always run the focused native Windows
  preflight, Windows binary smoke, and native installer tests without waiting
  for the all-package Windows suite or provisioning Bun solely for that suite.
  The complete Windows suite still runs on the default branch, by explicit
  manual opt-in, and in the exact-tag Release workflow.
- The Windows full-suite selection, package parallelism, and release rerun are
  executable release-trust contracts rather than documentation-only claims.
- The command-proof lock-deadline regression deterministically tests one total
  capped retry deadline without depending on host process-start latency; real
  Git snapshot behavior remains covered separately.
- Direct Go dependencies, pinned analysis tools, and GitHub-owned action SHAs
  are updated only to primary-source-verified stable releases. Module tidy,
  action allowlisting, integrity checks, and release ownership remain intact.
- Contributor, product, Makefile, and issue-report documentation describes the
  fast/full verification split and current source version accurately.
- Focused tests, release-trust, formatting, tidy, vet, static analysis, root and
  template tests, publication audit, and relevant workflow checks pass.

## Sub-Tasks

- [x] Add the bounded cached local verification target
- [x] Split candidate Windows preflight from conditional full-suite execution
- [x] Make the Git lock-deadline regression deterministic under host load
- [x] Refresh verified stable dependency, analysis, and action pins
- [x] Update contributor, product, Makefile, and issue-report documentation
- [x] Run focused and complete verification gates
- [x] Archive the completed TASK and commit the verified change

## Notes

- CI run `32554431282` completed the focused Windows preflight at 05:30:01Z,
  then spent until 05:47:35Z in the complete package suite. The exact-tag
  Release workflow already owns a separate complete native Windows gate.
- Whole-module coverage remains review evidence by explicit repository
  contract. Recent root and template measurements were reviewed without
  changing the semantic-test policy.
- Primary-source checks on 2026-08-22 found stable updates for checkout,
  setup-go, CodeQL, build provenance attestation, Staticcheck, Govulncheck,
  `golang.org/x/text`, and the test-only ECMAScript regex oracle.
- Focused lock-retry repetition, the complete race/release-trust gate, bounded
  whole-module coverage, tidy, vet, Staticcheck, Govulncheck, isolated pinned
  LangChain interoperability, and clean temporary-repository self-hosting all
  passed after the implementation.

## Deviations

None.
