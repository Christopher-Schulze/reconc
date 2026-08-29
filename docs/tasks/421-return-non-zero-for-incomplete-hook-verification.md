# TASK 421: Return non-zero for incomplete hook verification

## Why

`reconc hook verify` reports `complete: false` when transport, policy, response, or host evidence is degraded, but returns success after rendering the report. Shell and CI consumers using the command as a verifier therefore cannot distinguish complete verification without parsing the body.

## Acceptance

- A complete verification report exits zero.
- An incomplete or degraded verification report is still fully rendered and exits with one documented non-zero code.
- Report-generation or output failures remain distinguishable from a completed negative verification.
- Text and JSON tests cover complete, partially degraded, fully degraded, unsupported, live, and output-error paths.

## Sub-Tasks

- [ ] Define the hook-verification exit-code contract alongside existing CLI error codes.
- [ ] Preserve report output before returning the incomplete-verification status.
- [ ] Update CLI help and documentation.
- [ ] Run focused offline and live hook-verification tests.

## Notes

- Verified from finding 21.
- `runHookVerify` currently returns only `writeHookVerificationReport`; that writer returns nil after rendering regardless of `report.Complete`.
- The report fields are truthful today, but process status is not usable as a verification gate.

## Deviations
