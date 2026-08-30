# TASK 431: Fail closed on truncated adapter output

## Why

Generated OpenCode and Kilo adapters record output truncation but do not include it in their blocking-failure predicate. A truncated deny or error body can fail JSON parsing and be treated as allow.

## Acceptance

- Truncated pre-tool and Stop output always takes the platform's fail-closed response path.
- Post/observation routes retain their documented failure policy.
- Exact-limit output remains accepted; limit-plus-one is deterministically rejected.
- Generated-reference and Bun contract tests cover stdout, stderr, mixed output, UTF-8 boundaries, timeout, and nonzero exit.

## Sub-Tasks

- [x] Align generated `readCombined` status with route failure policy.
- [x] Add exact-limit and adversarial truncation fixtures for both adapters.
- [x] Regenerate expected references through existing tooling.
- [x] Run focused generated-adapter tests.

## Notes

- Verified from finding 114; OMP and Pi already treat truncation as blocking and provide the parity reference.
- The generated runner already rejected truncated Stop JSON in `continuationFrom`; the missing predicate affected blocking pre-tool and permission routes. Session routes use the predicate but retain their declared fail-open error policy, while observation routes do not call it.
- `make reference-docs` confirmed the generated command, hook, and schema references were already current.
- The focused generated transport contract passed for both adapters in 1.301 seconds; static OpenCode/Kilo reference checks passed in 0.012 seconds. The adversarial contract covers exact 8 KiB acceptance, 8 KiB plus one byte on stdout/stderr/mixed streams, a truncated multi-byte UTF-8 boundary, independent invalid UTF-8, nonzero exit, timeout, spawn failure, fail-open post observation, fail-closed permission, and rejected truncated Stop continuation.
- The pre-existing broad idle-continuation test exceeded the requested 30-second short-run ceiling and was terminated together with its two orphaned test sleeps. Its TASK-relevant truncated Stop branch is now covered by the focused 1-second transport contract; the full suite remains deferred to the queue-end gate.

## Deviations
