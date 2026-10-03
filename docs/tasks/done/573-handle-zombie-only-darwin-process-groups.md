# TASK 573: Handle zombie-only Darwin process groups

## Why

GitHub CI 37159531047 at ff9b13fe failed on macos-15 in
TestRunScriptTimeoutKillsProcessGroup: cleanup returned EPERM after timeout.
Linux checks, including races, passed for that commit. Apple's killpg1 excludes
zombies and returns EPERM when an existing group has no eligible members.
The same raw group-signaling operation is used by script cleanup and the MCP
boundary. Missing live processes must not become a false operational failure;
real permission failures must remain visible.

## Acceptance

- A real Darwin zombie-only process group reproduces the raw EPERM and completes
  script monitoring without a false cleanup failure.
- EPERM is normalized to ESRCH only after a fresh native group snapshot proves
  every member exited. A live member or inspection failure remains an error.
- Existing caller-cancel, timeout, resistant-descendant, kill-grace, and normal
  script outcomes remain valid. MCP signaling uses the same narrow semantics.
- No new dependency, process supervisor, polling service, retry policy, schema,
  user flag, or Windows behavior change is introduced.
- Relevant native tests and the complete non-race root/template suites pass;
  documentation is flushed, the TASK is archived, committed, and pushed alone.

## Sub-Tasks

- [x] Reproduce the native zombie-only group error and verify kernel/API semantics.
- [x] Share the minimal Unix signal primitive with Darwin-only native error classification.
- [x] Verify live/error preservation and script/MCP lifecycle regressions; flush docs.
- [x] Run relevant non-race validation, reread changes, archive, commit, and push.

## Technical Plan

Use the existing golang.org/x/sys/unix SysctlKinfoProcSlice API only after EPERM
on Darwin. Query kern.proc.pgrp for the exact owned group and inspect P_stat
against the SDK's SZOMB value. Return ESRCH only for an absent or zombie-only
snapshot; preserve real signal and inspection errors. Other Unix systems keep
their raw signal semantics. Existing callers retain their own ESRCH mapping.

One internal Unix Signal function serves the existing script TERM/KILL and MCP
signal/probe sites. This avoids two copies of the platform exception. Keep all
existing ownership, grace, cancellation, monitor joining, and dispatch behavior.
Use real child processes and native snapshots; no production test hooks or fake
process tables. The central race/static gate is separate from per-TASK checks.
It follows all implementation commits and pushes, together with the existing
isolated self-host and release-trust gates. It must pass before final delivery.

## Notes

CI: https://github.com/Christopher-Schulze/reconc/actions/runs/37159531047
Kernel source: https://github.com/apple-oss-distributions/xnu/blob/main/bsd/kern/kern_sig.c
Owners: internal/runtime/script_process_unix.go and
internal/mcpgateway/process_unix.go. Actual x/sys v0.47.0 signatures and native
SDK process-state definitions were read. No product tag or release is authorized.
The real Darwin regression failed on 783c25db with raw EPERM and the exact
monitor cleanup error. Its child exits naturally and is held unreaped to make
the kernel's zombie-only group state deterministic; no fake process table or
injected syscall result participates in this reproduction.
The fixed monitor regression passes. A second real zombie fixture verifies MCP's
group-existence probe and reap/close path. The real test runner's live group
retains EPERM; invalid signal and unsafe group-ID guards preserve EINVAL.
Apple's sysctl_prochandle includes live and zombie lists, filters PGRP by the
exact native group ID, and propagates read errors rather than returning a partial
successful snapshot. Script/MCP lifecycle tests run against actual process groups.
The shared primitive remains synchronous so cancellation cannot cancel its own
cleanup; existing lifecycle owners retain grace timing and monitor joining.
The full non-race gate caught the missing architecture package-map entry; that
entry is now propagated. CI 37161321537 at 783c25db reproduced the same native
EPERM in the resistant-descendant fixture's cleanup, after its behavioral
assertions passed. Cleanup now uses the shared primitive; the actual child
liveness, outcome, and mutation assertions are unchanged.
Final focused regressions and the full isolated-HOME make test-fast passed for
both root and portable-template modules; make build passed. Code, documentation,
and the final diff were reread. No per-TASK race or static run was started.
The central final gate follows all implementation and documentation TASK commits.

## Deviations

None.
