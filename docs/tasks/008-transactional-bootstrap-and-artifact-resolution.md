# TASK 008: Transactional bootstrap and artifact resolution

## Why

Bootstrap is currently a minimal best-effort installer and assumes platform
configuration or versioned binaries already exist. New repositories need an
AI-operable, non-destructive transaction while `BOOTSTRAP.md` remains the
detailed recovery tutorial and verification checklist.

## Acceptance

- Bootstrap supports inspect, plan, apply, and verify phases with deterministic machine-readable output.
- A failed apply restores the exact prior repository state or leaves a verified non-destructive candidate without deleting user files.
- Profiles install policy, TASK workflow, selected packs, hooks, wrappers, ignores, and docs only when applicable and explicitly selected.
- Binary resolution is platform-correct, version-independent where safe, checksum-verifiable, and diagnostic when unavailable.
- Re-running bootstrap is idempotent and reports drift without overwriting repository customizations.
- `harness/template/BOOTSTRAP.md` remains a complete AI tutorial, manual recovery path, and parity checklist for the CLI transaction.

## Sub-Tasks

- [ ] Define the bootstrap plan and manifest contract.
- [ ] Implement non-destructive apply, rollback, idempotence, and verification.
- [ ] Integrate profiles, packs, TASK lifecycle, adapters, wrapper, and ignore policy.
- [ ] Repair binary and artifact discovery across supported platforms.
- [ ] Synchronize `BOOTSTRAP.md`, fixtures, end-to-end tests, and failure injection.

## Notes

Approved areas: 14 Transactional bootstrap while preserving tutorial;
23 Binary/artifact resolution.

## Deviations

None.
