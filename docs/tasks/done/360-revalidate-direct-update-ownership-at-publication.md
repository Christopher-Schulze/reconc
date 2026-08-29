# TASK 360: Revalidate direct-update ownership at publication

## Why

Direct update verifies the installed binary before download, attestation, and smoke testing, but publication later replaces the path without proving it still names that verified binary. A concurrent replacement can be overwritten.

## Acceptance

- Direct-update publication is conditional on the installed binary retaining its validated identity and digest.
- Replacement, symlink substitution, or type changes during preparation fail closed.
- Backup and rollback data describe the exact displaced generation.
- Tests cover replacement at each long-running update phase and immediately before publication.

## Sub-Tasks

- [x] Carry expected installed-binary identity through direct update.
- [x] Make backup and publication conditional on that identity.
- [x] Add adversarial replacement and rollback tests.
- [x] Run focused update and global installation tests.

## Notes

- Verified from OMP session `01a04cc5-6a01-7526-95bb-b2715553daf3`, finding #106.
- Initial evidence: `internal/usercli/update.go` checked ownership before remote preparation and did not revalidate the old target at publication.
- Implemented a streaming conditional publication primitive that binds target identity, metadata, size, modification time, and SHA-256 before staging and immediately before replacement. Direct updates capture the receipt-owned generation before candidate preparation and retain it for rollback.
- Deterministic phase hooks exercise regular-file replacement during candidate materialization, attestation, smoke testing, and immediately before publication, plus symlink and directory substitution. Receipt mutation after publication proves exact binary rollback while preserving the replacement receipt.
- Focused gates passed: `go test ./internal/atomicfile -run 'TestWrite(Stream)?IfCurrent' -count=1 -timeout=30s`; direct update and lifecycle tests in `internal/usercli`; `gofmt`; `git diff --check`. Per explicit execution instruction, race, release-trust, full `make test`, and local Windows runs were not executed.

## Deviations

The repository-wide race, release-trust, and other heavy gates remain available but were not run because the user explicitly restricted them to on-request execution. Windows code and tests remain present; local execution was limited to the current POSIX host.
