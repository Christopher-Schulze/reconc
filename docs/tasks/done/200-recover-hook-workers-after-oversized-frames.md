# TASK 200: Recover hook workers after oversized frames

## Why

The hook worker's bounded frame reader returns immediately when a newline frame
exceeds its limit. `runHookWorker` treats that read error as terminal, so one
oversized request kills the persistent worker and forces client fallback or
restart. The unread remainder is not consumed, making same-stream recovery
undefined.

## Acceptance

- An oversized frame is drained through its terminating newline under a bounded
  strategy, produces one deterministic protocol error when response identity is
  recoverable, and does not desynchronize the next valid frame.
- EOF, missing newline, invalid UTF-8/JSON, buffer-full fragments, and repeated
  oversized frames have explicit terminal or recoverable behavior.
- Drain work and retained memory are bounded independently of attacker frame
  length; cancellation remains effective.
- End-to-end tests send oversized then valid frames and prove the same worker
  continues safely without evaluating truncated payloads.
- Client fallback behavior remains reserved for genuine worker/protocol loss.

## Sub-Tasks

- [x] Specify recoverable and terminal frame errors
- [x] Implement bounded drain and error-response handling
- [x] Preserve frame identity and stream synchronization
- [x] Add adversarial stream and worker-lifecycle tests
- [x] Run hook-worker, E2E, race, and complete gates

## Notes

- Verified in `readHookWorkerFrameLimit` and `runHookWorker` in
  `internal/cli/hook_worker.go`.

## Deviations

None.
