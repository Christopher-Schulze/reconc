# TASK 511: Enforce semantic evidence contracts for built-in recipes

## Why

The four TASK 498 evidence recipes currently validate metadata and required
fields, but an arbitrary script can still satisfy every recipe by exiting 0.
The recipe boundary must enforce the invocation identity needed by each
contract and exercise realistic positive and negative workflows without
creating a second project-specific policy engine.

## Acceptance

- Each built-in evidence recipe has a typed contract that rejects missing,
  mutable, or incompatible invocation arguments at policy compile time.
- Public API and generated-artifact checks bind immutable base/current identity;
  migration checks require an isolated engine/database and explicit forward plus
  rollback policy; performance checks require attributable baseline/result/output
  and suite inputs.
- Runtime tests execute contract-aware fixture scripts that inspect their real
  arguments and input context; a generic exit-0 script is rejected by the
  recipe boundary.
- Existing ordinary `require_script` rules and user templates remain compatible;
  lockfile output stays deterministic and schema-valid.
- Documentation, catalog output, focused tests, full repository gates, and one
  local TASK commit are complete. No version bump, tag, or publication.

## Sub-Tasks

- [x] Model typed recipe contracts and validate required invocation identities.
- [x] Wire contract metadata through parsing and lock/runtime evaluation.
- [x] Add realistic fixture workflows and negative contract tests.
- [x] Update documentation, run all gates, archive, commit, and push.

## Notes

- Discovered during the TASK 470-499 reality audit; TASK 498 acceptance was
  previously satisfied only by metadata and opaque script exit status.
- Preserve the existing project-owned-script boundary: Reconc validates the
  contract envelope and identity binding, while each repository remains the
  authority for its API tool, migration engine, generator, and benchmark tool.
- Added real Git-candidate API comparisons, generator output comparisons with
  preservation of unrelated changes, and SQLite forward/rollback schema/data
  round trips. Focused integration tests pass, including failure cases.
- Benchmark `compare --recipe` now produces the contract envelope from the
  actual comparison, binds the persisted report hash, and rejects mismatched
  source/suite, dirty results, and incompatible CPUs. Passing and allocation
  regression tests exercise the complete parser/comparator/output path.
- Contract negative tests reject mutable identities, disabled migration flags,
  duplicate flags/JSON keys, options after `--`, mismatched stdout identities,
  and unsafe or incomplete output lists. Generic exit-0 recipes block.
- `make test-fast`, `make test`, `make vet`, `make lint`, and
  `git diff --check` pass. The complete test gate includes publication audit,
  uncached root and portable-template race suites, and release-trust.
- `make self-host` passes in its isolated temporary repository. Reference docs
  and publication checks pass as part of the required gates. The task overview
  retains the last ten completed entries; older archived details are preserved.

## Deviations

None planned.
