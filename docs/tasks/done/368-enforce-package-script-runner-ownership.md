# TASK 368: Enforce package-script runner ownership

## Why

A package-scripts gate validates the detected package manager but can accept success evidence from a command using another supported runner. Manager-scoped evidence can therefore be satisfied by `pnpm`, `yarn`, or `bun` when the gate requires `npm`, or vice versa.

## Acceptance

- Candidate script commands use the gate's configured or detected package manager.
- Success evidence from a different runner cannot satisfy the gate.
- Manager aliases or invocation variants are accepted only when explicitly equivalent.
- Tests cover every supported manager and mismatched-runner evidence.

## Sub-Tasks

- [x] Define canonical manager-to-runner ownership rules.
- [x] Restrict package-script candidates and evidence matching accordingly.
- [x] Add cross-manager false-positive regressions.
- [x] Run focused package-scripts assurance tests.

## Notes

- Verified from OMP session `01a04cc5-6a01-7526-95bb-b2715553daf3`, finding #114.
- Current evidence: `internal/assurance/package_scripts.go` checks manifest manager ownership separately from runner selection in command candidates.
- Package-script candidates are now filtered to the configured manager or the sole detected/inherited manager before success evidence is compared. Runner names are canonicalized case-insensitively with whitespace normalization; no unlisted alias is accepted.
- Regression coverage exercises Bun, npm, pnpm, and Yarn, rejects mismatched runner evidence, and accepts the explicit case-equivalent runner form. `go test ./internal/assurance -count=1 -timeout=120s`, `go vet ./internal/assurance`, `make fmt-check`, and `make reference-docs-check` passed.

## Deviations

- The repository-wide race, release-trust, publication, and other heavy suites were not run, per the explicit execution constraint. Windows-specific tests were not run locally; cross-platform source compatibility remains covered by CI.
