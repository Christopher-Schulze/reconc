# TASK 572: Reuse the policy evaluator within completion gates

## Why

Finding F8: completion calls the package-level lock validation and policy
evaluation functions, each of which constructs an empty evaluator. The existing
immutable plan cache can eliminate duplicate decoding/preparation while still
checking lock and source freshness on both accesses.

## Acceptance

- One completion attempt uses one runtime.Evaluator for lock validation and
  policy evaluation; all current before/after candidate checks remain present.
- Source or lock changes between accesses invalidate/reject the cached plan.
- No shared/global cache, new dependency, public flag, or schema change is introduced.
- A relevant completion benchmark compares the old and new path under identical
  conditions; any reported speed/allocation claim is measured and scoped.
- Targeted completion/runtime cache tests, the root suite, vet, lint, and
  development build pass. Architecture documentation reflects local plan reuse.
- The completed TASK is archived, committed, and pushed to origin/main.

## Sub-Tasks

- [ ] Inspect existing evaluator cache invalidation and completion benchmark coverage.
- [ ] Reuse one existing evaluator locally for the two accesses without skipping freshness.
- [ ] Verify policy/source drift and candidate drift, measure the relevant path, and flush docs.
- [ ] Run validation, review all changes, archive, commit, and push.

## Technical Plan

In completiongate.evaluateOnce, create runtime.NewEvaluator once and call its
ValidatePolicyLockfile and CheckRepoPolicy methods. Do not retain it across
independent attempts or change completion state identity capture. The runtime
cache verifies the lock hash and source freshness before reuse; retain those
checks and its fail-closed behavior unchanged.

Use existing runtime-plan invalidation tests and completion drift tests. Reuse
the repository's benchmark tooling or a narrowly scoped Go benchmark if an
appropriate completion benchmark is missing. Capture baseline before the edit
and comparison after it, with the same fixture and iteration settings. Do not
rewrite the checked benchmark baseline or assert universal speedups. Restrict
documentation to the actual local lifetime and continuing freshness guarantee.

## Notes

Source owners: internal/completiongate/gate.go,
internal/runtime/lockfile.go, evaluator_core.go, runtime_plan.go.
F7 is intentionally excluded: its current archive ownership is documented.

## Deviations

None.
