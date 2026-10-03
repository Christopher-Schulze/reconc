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

- [x] Inspect existing evaluator cache invalidation and completion benchmark coverage.
- [x] Reuse one existing evaluator locally for the two accesses without skipping freshness.
- [x] Verify policy/source drift and candidate drift, measure the relevant path, and flush docs.
- [x] Run validation, review all changes, archive, commit, and push.

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
The existing cache compares lock bytes and observes live source freshness before
reuse. Completion's before/after capture, typed drift retry, and receipt checks
remain untouched. No completion benchmark exists; a real isolated repository
with 256 deny-write rules measures the complete public Evaluate path, including
candidate capture and report construction. Record the unchanged path first.
The production change is one local NewEvaluator and two method calls. The cache
loader still reads the lock bytes, hashes them, and observes source freshness on
the second access; the before/after candidate and proof checks are unchanged.

On Apple M1, Go 1.27.1 darwin/arm64, five repetitions per path used the identical
BenchmarkCompletionPolicy256 fixture and command:
go test ./internal/completiongate -run '^$' -bench '^BenchmarkCompletionPolicy256$' -benchmem -benchtime=1s -count=5.
Setup/compilation is outside the timer; Evaluate is real and every report must pass.
Baseline ff9b13fe: ns/op 67415342, 52559835, 47387892, 50203806, 47315649;
B/op 11007471, 11000969, 11010568, 11021950, 11000388;
allocs/op 109149, 108967, 108893, 108951, 108887.
With local reuse: ns/op 44319086, 45835346, 44434792, 44556918, 45257675;
B/op 7409960, 7384770, 7429835, 7397330, 7413279;
allocs/op 77834, 77882, 77887, 77864, 77888.
Medians: 50.204 to 44.557 ms, 11007471 to 7409960 B/op, and 108951 to
77882 allocs/op. These are local observations for a non-Git 256-rule repository,
not a product-wide latency guarantee. No checked benchmark baseline is rewritten.
The complete completion package and targeted runtime-plan invalidation, lifecycle,
and report-parity tests passed uncached. Vet, pinned staticcheck, and the
development build passed for the current change. Architecture docs are flushed.
The complete isolated-HOME make test-fast passed for both Go modules. All
modified files and the final diff were reread and reviewed. The user's latest
instruction requires individual TASK commits/pushes and one central final race
and static gate after all implementations, rather than per-TASK repetition.
The prematurely started aggregate race run was canceled for that sequencing;
it is not recorded as passed. Automatic Windows suites remain excluded.

## Deviations

None.
