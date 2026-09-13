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

- [x] Define the component state table and read-only planning/report schema.
- [x] Move skill inspection before binary fast returns and implement owned skill-only updates.
- [x] Integrate target bundle verification and coordinated transaction/recovery.
- [x] Add actual CLI state-machine and fault-injection regressions.
- [x] Update help, schemas, guide, and lifecycle documentation; complete final gates.

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

Verified baseline functions: `update(ctx, currentVersion, request, apply)`, `applyDirectUpdate`, `CheckUpdate`, and `ApplyUpdate`. The current LifecycleReport has no skill component. TASK 551 has shipped `--skill-only`, `--no-skill`, and the receipt-owned skill bundle; update still ignores that component.

State table for implementation: no skill receipt and no destination is `missing` and yields an explicit `install-cli --skill-only` proposal; no receipt with occupied destination is `unmanaged`; a receipt with a missing destination remains `missing` and requires explicit repair; matching receipt files with a different selected-release manifest are `stale-owned` and update automatically; matching files and target manifest are `current`; mismatched or linked files are `modified-owned` and block mutation. A selected release without a valid skill manifest/archive is `unavailable` for the skill component; disabled or shadowed host discovery is reported separately and never alters ownership. Binary selection and skill state are independent, including same-artifact release selection.

The published global-lifecycle v1 schema is immutable and forbids extra fields. Add a v2 lifecycle contract with an optional typed skill component and an explicit action carrying `argv`, `cwd`, and authorization; retain v1 as legacy. Keep global-diagnostic v1 and its existing action shape unchanged. The selected release's checksummed skill manifest supplies the target digest for read-only checks; apply additionally verifies the bounded ZIP and its files. A missing bundle never permits an owned-skill update. Binary and skill publication share the installation lock and receipt transition, with a verified backup and explicit partial-failure state.

Implementation: `update check` now classifies the selected release skill before a same-binary return. Missing skills produce an explicit typed skill-only install action; stale owned skills update under the receipt lock with or without a binary change. The v2 lifecycle report preserves v1 as a legacy schema. Bounded release manifest and ZIP verification is followed by candidate `skill-manifest --json` equality before publication. Conditional binary rollback is bound to the published file identity; an external replacement is preserved and the previous binary backup retained with `changed=true` on partial failure. Real CLI, combined update, stale-only update, checksum-valid bundle mismatch, concurrent explicit install, external replacement, and receipt-failure regressions pass. The first complete `make test` run found a missing command-list entry in documentation; that entry was corrected, and the final complete run passed after rollback hardening.

Completion evidence (2026-09-13, macOS arm64, Go 1.27.1): `make test` passed publication auditing, uncached race suites for both modules, and release trust with isolated real release artifacts (`release-trust: ok`, 69-second target). `make vet`, `make lint`, `make build`, and `make self-host` passed on the final code; self-host was repeated after build. `git diff --check` passed. No product tag or publication was created. Hosted Linux and integrated host qualification remain owned by TASK 555.

## Deviations

None.
