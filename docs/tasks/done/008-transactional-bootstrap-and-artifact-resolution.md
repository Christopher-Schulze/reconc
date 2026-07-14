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

- [x] Define the bootstrap plan and manifest contract.
- [x] Implement non-destructive apply, rollback, idempotence, and verification.
- [x] Integrate profiles, packs, TASK lifecycle, adapters, wrapper, and ignore policy.
- [x] Repair binary and artifact discovery across supported platforms.
- [x] Synchronize `BOOTSTRAP.md`, fixtures, end-to-end tests, and failure injection.

## Notes

Approved areas: 14 Transactional bootstrap while preserving tutorial;
23 Binary/artifact resolution.

Design contract: `bootstrap inspect` is read-only; `bootstrap plan` emits a
versioned deterministic manifest and writes it only with an explicit output
path; `bootstrap apply` consumes that exact plan or builds the same plan from
explicit selections; `bootstrap verify` proves desired hashes, modes,
lockfile freshness, and artifact resolution without mutation. The `minimal`
profile owns only policy and agent orientation. The `governed` profile adds
the TASK control plane, documentation, runtime ignores, and the repo-local
wrapper. Packs and hook kinds remain explicit selections; stack and platform
detection can recommend them but never selects them.

Apply is create-only for repository targets. Exact existing artifacts are
unchanged. Non-matching existing files are never replaced: apply materializes
hash-addressed `.reconc-candidate-*` files and stops before installing any
other target. Fresh targets are staged, hash-verified, and published without
replace semantics; injected or real failures roll back only files and empty
directories created by that transaction. The compiled lockfile is produced
last and verified read-only.

Binary installation accepts the already-running executable or an explicit
local artifact plus lowercase SHA-256. It publishes a stable
`reconc-<os>-<arch>[.exe]` repo-local name. Generated shell resolvers prefer
that stable name, accept exactly one compatible versioned release artifact,
and fail with an ambiguity diagnostic when several versions match. PATH is
the final fallback. No runtime download or network dependency is introduced.

Implementation evidence: `internal/bootstrap` owns versioned inspect, plan,
report, and verification contracts; create-only publication uses same-directory
staging, fsync, checksum verification, hard-link no-replace semantics, and
identity-plus-checksum rollback. The CLI exposes explicit phases and retains a
transactional legacy shorthand. Governed and minimal round trips, drift
candidates, stale plans, injected failure rollback, symlink resistance,
binary checksums, artifact ambiguity, stack applicability, generated-wrapper
parity, and shell completion are covered by package and CLI tests.

Final proof: root tests and race tests pass with `-count=1`; `go vet`,
Staticcheck, host build, ShellCheck, generated-scaffold idempotence, and
`git diff --check` pass. The nested template harness test suite passes. A fresh
Git repository completed governed plan/apply/verify with Codex, git pre-commit,
and stable binary installation; the immediate explicit re-apply created zero
files and reported every selected artifact unchanged.

## Deviations

None.
