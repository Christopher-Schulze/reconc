# TASK 264: Perform the v0.9.7 release general rehearsal

## Why

The accumulated v0.9.7 source candidate needs one final source-first review
before any release authorization. Passing gates alone do not prove that error
paths, scripts, documentation, generated surfaces, portability contracts, and
test assertions remain mutually consistent after the recent task sequence.

## Acceptance

- Every tracked production, test, script, workflow, and documentation surface
  is included in a bounded audit inventory, with high-risk implementation
  patterns reviewed directly in source.
- Every reproducible defect or inconsistency found by the review is corrected
  at its owning layer; behavior defects receive a failable regression.
- Generated references, release-candidate wording, task state, scripts, and
  contributor guidance agree with the final source behavior.
- Formatting, module tidiness, unit and integration tests, race tests, Vet,
  Staticcheck, Govulncheck, coverage, self-hosting, publication, release-trust,
  LangChain interoperability, and platform build checks pass on the final tree.
- The final diff contains no unrelated cleanup, release tag, published release
  artifact, version change, branch creation, or unrequested push; local `main`
  remains the only branch.

## Sub-Tasks

- [x] Inventory the complete repository and release invariants
- [x] Audit production code, tests, scripts, workflows, and documentation
- [x] Reproduce and correct every validated finding
- [x] Run focused regressions and the complete local release gates
- [x] Re-read the final diff and archive the TASK for a verified local commit

## Notes

- The starting tree is clean at `f79682f9`; local and remote repositories expose
  only `main`, and v0.9.7 remains an unreleased source candidate.
- Graphify has no existing graph for this checkout. A new graph would exceed its
  whole-corpus narrowing threshold and require delegated semantic extraction,
  so direct source and gate evidence remains authoritative for this exhaustive
  audit.
- The baseline fast suite, Vet, Staticcheck, module-tidiness checks, and
  Govulncheck pass. Direct source review rejected scanner noise around bounded
  conversions, controlled process arguments, protected private state, and
  intentional best-effort cleanup instead of changing valid behavior.
- Validated findings are limited to an outdated direct `x/term` dependency,
  ambiguous empty `CDPATH` assignments in three POSIX launchers, and missing
  whole-repository shell-syntax enforcement in release trust. The generated
  hook wrapper also needed a localized ShellCheck annotation for its deliberate
  second-line receipt probe.
- `x/term` is updated to the current compatible release. All tracked shell
  entrypoints pass ShellCheck and interpreter syntax validation; Release Trust
  now enumerates tracked shebang files and validates Bash and POSIX shell
  sources automatically. Launcher changes are included in the regenerated,
  deterministically verified advanced harness pack.
- The complete root and template race suites, release artifact and tamper
  checks, all registered fuzz targets, coverage, formatting, generated
  references, module tidiness, Vet, Staticcheck, Govulncheck, self-hosting,
  publication audit, and all five release-target cross-builds pass.
- The first local LangChain invocation correctly rejected the machine's global
  Python 3.14 environment because the external-consumer contract requires
  Python 3.13.14 and exact packages. The proof then passed in a disposable
  Python 3.13.14 environment installed from the hash-pinned lock; the temporary
  environment was removed. This is expected prerequisite enforcement, not a
  Reconc defect.
- No production behavior bug, security vulnerability, documentation drift,
  race, generated-surface drift, or release-contract inconsistency remained
  after direct source review and the final gates.

## Deviations

None.
