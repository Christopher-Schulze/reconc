# TASK 506: Reject deleted applicable module manifests in assurance

## Why

Assurance module selection uses manifests detected in the current checkout. A changed Go, Rust, or Python source tree whose applicable module manifest was deleted can therefore lose its module owner and silently fall back to skipped or repository-root verification.

## Acceptance

- A changed applicable Go, Rust, or Python module manifest that is missing, replaced by a symlink, non-regular, or unreadable fails closed.
- Existing root, nested, workspace, and unchanged-module selection behavior remains unchanged.
- The error identifies the affected manifest and gate context; no deleted manifest is converted into skipped assurance.
- Regression tests cover deleted applicable manifests and existing module-scope behavior; required repository gates pass.

## Sub-Tasks

- [x] Bind changed applicable module-manifest paths to regular-file verification before module selection can skip them.
- [x] Add deleted and replacement regressions without weakening existing module ownership tests.
- [x] Run assurance/runtime gates, flush docs, archive this task, commit and push.

## Notes

- Discovered during the read-only reality audit of TASK 483.
- `selectModuleScopes` now verifies changed applicable Go, Rust, and Python manifest paths before current-checkout module matching; deleted and non-regular manifests fail closed with their exact path.
- Focused assurance tests and the targeted race test pass.
- Existing assurance documentation already stated the missing, symlinked, and unreadable-manifest fail-closed contract; no wording change was required.
- Verification: `make test-fast` passed with an isolated temporary `GOCACHE` after one global-cache artifact failure; `make test`, `make vet`, `make lint`, `make self-host`, focused assurance tests, targeted assurance race tests, and `git diff --check` passed.

## Deviations

None planned.
