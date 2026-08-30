# TASK 417: Preserve exact hook output-envelope limits

## Why

`boundHookResult` accepts an 8 KiB envelope, but runtime emission adds a newline before the worker captures stdout in an 8 KiB buffer. An exactly-at-limit valid deny envelope is therefore truncated and replaced with a generic worker failure.

## Acceptance

- Envelope admission includes every emitted framing byte.
- A deny at the exact boundary reaches stdout as valid complete JSON and retains the intended host decision.
- Oversized output emits a minimal valid bounded deny/error contract rather than truncated JSON.
- Boundary tests cover limit-minus-one, exact limit, limit-plus-one, multi-byte UTF-8, newline, and every stdout-decision host.

## Sub-Tasks

- [x] Define whether the shared limit includes the terminal newline.
- [x] Align result bounding, runtime emission, worker capture, and wrapper parsing.
- [x] Add exact-boundary and fail-closed host regressions.
- [x] Run focused hook result and worker tests.

## Notes

- Verified from finding 99.
- Current `fmt.Fprintln` makes an accepted 8192-byte body consume 8193 captured bytes.
- Confirmed on current source: normal stdout/stderr halves are bounded by body bytes only, and the fail-closed fallback admits an envelope whose body alone equals the complete route budget. Runtime emission then appends one newline to every non-empty stream, while worker and offline-verification capture enforce the unexpanded 8 KiB ceiling.
- The route budget must include the one-byte newline emitted for each non-empty stream. Wrapper command substitution may remove that byte later, but capture admission must account for it first.
- Bounding now reserves one frame byte in each non-empty stream allocation and admits a minimal fail-closed envelope only when its body plus terminal newline fits. The shared runtime emitter documents and applies the same framing used by worker and one-shot capture.
- Regression coverage pins minimal-envelope limit-minus-one, exact-limit, and limit-plus-one behavior for Cursor, GitHub Copilot, and Grok pre-tool, permission, Stop, and SubagentStop shapes; normal-body boundaries additionally cover UTF-8 byte counts and pre-existing newlines.
- Verification passed: focused hook-result and hook-worker tests, the complete `internal/cli` package, and `make test-fast`.

## Deviations
