# TASK 421: Return non-zero for incomplete hook verification

## Why

`reconc hook verify` reports `complete: false` when transport, policy, response, or host evidence is degraded, but returns success after rendering the report. Shell and CI consumers using the command as a verifier therefore cannot distinguish complete verification without parsing the body.

## Acceptance

- A complete verification report exits zero.
- An incomplete or degraded verification report is still fully rendered and exits with one documented non-zero code.
- Report-generation or output failures remain distinguishable from a completed negative verification.
- Text and JSON tests cover complete, partially degraded, fully degraded, unsupported, live, and output-error paths.

## Sub-Tasks

- [x] Define the hook-verification exit-code contract alongside existing CLI error codes.
- [x] Preserve report output before returning the incomplete-verification status.
- [x] Update CLI help and documentation.
- [x] Run focused offline and live hook-verification tests.

## Notes

- Verified from finding 21.
- `runHookVerify` currently returns only `writeHookVerificationReport`; that writer returns nil after rendering regardless of `report.Complete`.
- The report fields are truthful today, but process status is not usable as a verification gate.
- Confirmed at implementation: complete reports exit 0, fully rendered incomplete reports exit 2, and report-generation or output failures exit 1.
- Added table-driven text and JSON coverage for complete, partially degraded, fully degraded, and unsupported reports, plus live missing-host, live operator-abort, help, and output-error coverage.
- Focused CLI tests, command-metadata tests, formatting, generated-reference validation, and `git diff --check` passed.

## Deviations

- Per the operator's short-run instruction, repeated full, race, release-trust, and platform suites were deferred to the single queue-end gate run; no Windows tests were run locally.
