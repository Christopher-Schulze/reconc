# TASK 461: Keep self-host runtime contracts current

## Why

The final macOS CI self-host gate invokes the global Kimi runtime with the retired one-argument contract. Production and generated Kimi hooks require the receipt-bound `receipt-v1 <event>` contract, so the golden-path script fails after correctly installing and verifying the current hook configuration.

## Acceptance

- The self-host golden path invokes Kimi through the same receipt-bound contract generated for users.
- The Kimi runtime contract remains covered by focused CLI tests and the complete isolated self-host flow.
- Focused CLI and hook tests, the complete isolated self-host flow, and the generated harness-pack check pass without unexpected diagnostics.

## Sub-Tasks

- [x] Reverify the failing CI invocation against the current CLI and hook generator contracts.
- [x] Update the isolated self-host caller without weakening receipt validation.
- [x] Run focused Kimi and generated pre-commit tests plus the isolated self-host gate.

## Notes

- GitHub Actions run `33321929178`, job `99285295070`, failed in `make self-host` after the full macOS package tests passed.
- The failure is deterministic: `scripts/tests/self-hosting.sh` calls `hook kimi-runtime kimi-session-start`, while `runKimiCodeRuntime` and `generateKimiCode` require `hook kimi-runtime receipt-v1 kimi-session-start`.
- The first focused rerun proved the runtime identity invariant as well: directly executing the governed repository's stable artifact is correctly rejected because the generated global hook resolves the receipt-owned bare `reconc` from `PATH`. The self-host caller now exercises that production path.
- The next self-host run reached success but exposed `reconc_candidate_trusted: command not found` from the generated Git pre-commit hook. The shared binary resolver called a helper owned only by the agent wrapper. The helper is now emitted by every resolver consumer, and pre-commit applies it consistently to dev, release, and `PATH` candidates.
- The same clean-output pass showed the generic self-host fixture did not satisfy Devin's native `hook_event_name` contract and therefore only exercised its fail-open warning. The fixture now sends the documented Devin SessionStart payload so the golden path verifies real normalization without diagnostics.
- Final focused verification passed: `go test ./internal/hooks ./internal/cli -count=1`, `make self-host`, `go run ./scripts/build/harness-pack --check`, and `git diff --check`.

## Deviations
