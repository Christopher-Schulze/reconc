# TASK 552: Coordinate binary and skill updates

## Why

reconc update currently updates the binary and installation receipt only. It returns early when the selected binary is current, so a missing or stale skill would never be handled there. The requested behavior is to suggest installation when absent and update an existing owned stale skill automatically.

## Acceptance

- update check inspects binary and skill independently without mutation, including the same-binary case.
- Bare reconc update updates a stale owned skill together with the selected binary, and also repairs a stale owned skill when the binary already matches.
- A missing skill produces a precise installation proposal first, with typed argv/cwd/authorization. It is not installed silently by update.
- Current, missing, stale-owned, modified-owned, unmanaged, disabled/shadowed, and unavailable states remain distinguishable.
- Existing ownership, downgrade, checksum, provenance, offline-update, locking, and rollback protections remain intact.
- Reports cannot label a partially failed binary/skill transaction fully current.

## Sub-Tasks

- [ ] Define the component state table and read-only planning/report schema.
- [ ] Move skill inspection before binary fast returns and implement owned skill-only updates.
- [ ] Integrate target bundle verification and coordinated transaction/recovery.
- [ ] Add actual CLI state-machine and fault-injection regressions.
- [ ] Update help, schemas, guide, and lifecycle documentation.

## Technical Plan

1. Extend `internal/usercli/update.go`, `lifecycle.go`, `diagnostic.go`, receipt/transaction helpers, and `internal/cli/lifecycle_cmd.go`. Preserve CheckUpdate/ApplyUpdate separation and the existing typed DiagnosticAction model.
2. Inspect skill state before same-version/same-artifact early returns. Determine target bundle identity from the verified selected release, not the currently running binary's old embedded skill. Compare complete content digests rather than product version strings.
3. Use TASK 551's deterministic release bundle and verified manifest. For a downloaded target, require the bundle to belong to the same verified release/candidate; materialize it through the existing bounded artifact transport and verifier. For offline `--from-dir`, require the matching bundle when repair is needed and return exact remediation if absent. Do not execute an unverified candidate or recursively acquire the receipt lock through install-cli.
4. State decisions: current binary/current skill is unchanged; new binary/current-or-stale owned skill installs the matching target components; current binary/stale owned skill updates only the skill; absent skill proposes the TASK 551 skill-only action; modified/unmanaged skill is preserved with a conflict action. A discovery-disabled or shadowed current skill is a discovery issue, not a reason to overwrite another copy.
5. Keep update check and JSON/noninteractive output free of prompts and mutations. Print the missing-skill proposal in human output and expose the exact action in JSON. A caller explicitly selecting that action authorizes installation; elapsed time or a default answer does not.
6. Stage and verify both candidates before publication. Under the shared lock revalidate original receipts, binary and skill identities, then publish with recoverable backups and component results. Inject failures at every boundary, including rollback failure; return the true remaining state and recovery action. Never set Changed false if an unrecovered component changed.
7. Preserve source/package-manager ownership restrictions for binary update. Still diagnose skill status when binary update is unavailable; skill repair must use a verified payload and must not convert binary ownership. Old releases without a skill bundle are explicitly unsupported for that component, never falsely current.
8. Keep lifecycle/schema compatibility explicit and update typed actions, help, completion, and documentation. Do not add a parallel update command or require an MCP server for this flow.

## Verification

Run `go test ./internal/usercli ./internal/cli ./internal/schema` and `make test-release-trust`. Exercise the Cartesian cases that change decisions: same/new binary with absent/current/stale/edited skill; offline missing/wrong bundle; foreign receipt; downgrade; concurrent install/update; interrupted staging/publication; receipt write failure; rollback failure; redirected target path; and no-TTY JSON. Snapshot filesystem contents and metadata around update check to prove it does not mutate.

## Dependencies

TASK 551. TASK 553 verifies the resulting agent-facing action flow; TASK 555 verifies combined installation/update end to end.

## Notes

Verified baseline functions: `update(ctx, currentVersion, request, apply)`, `applyDirectUpdate`, `CheckUpdate`, and `ApplyUpdate`. The current LifecycleReport has no skill component. All command options proposed by TASK 551 remain unavailable until implementation.

## Deviations

None.
