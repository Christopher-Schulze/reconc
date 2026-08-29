# TASK 458: Reuse the doctor Grok capability probe

## Why

One deep-doctor report calls the Grok native-Stop capability probe independently from both the runtime and leader-steering checks. Each probe reads and scans the installed Grok guide, and the two checks can report contradictory capability states if the installation changes between reads.

## Acceptance

- `buildDoctorDeepReport` resolves the Grok native-Stop capability at most once per report.
- Both Grok checks consume the same immutable probe result without changing standalone check semantics.
- Reports cannot contain runtime and leader-steering conclusions derived from different capability snapshots.
- Deterministic tests count probe invocations and cover supported, unsupported, missing-hook, and undiscovered-repository paths.

## Sub-Tasks

- [ ] Define one report-scoped capability snapshot and pass it only to the checks that consume it.
- [ ] Preserve direct unit-test entry points without allowing them to re-probe within a built report.
- [ ] Add invocation-count and consistent-result regressions around deep-doctor assembly.
- [ ] Run focused deep-doctor and Grok capability tests.

## Notes

- Verified from worker finding 845 against current source.
- `doctorCheckGrokRuntime` calls `doctorProbeGrokNativeStop` after inspecting native routes; `doctorCheckGrokLeaderSteering` calls it again for a compatible leader.
- `grokacp.ProbeNativeStopGate` resolves the Grok home, opens the installed guide, verifies file identity, reads up to 1 MiB, and scans its capability markers on every call.
- Worker findings 744 and 802 were rejected: `Run` wraps both output streams in `trackedOutputWriter` and joins any short-write or writer error into the command result, so the ignored local `Encode` returns do not produce exit 0. Their lack of command-local error context is not the claimed correctness failure.

## Deviations
