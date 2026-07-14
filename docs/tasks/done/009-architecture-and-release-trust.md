# TASK 009: Architecture and release trust

## Why

Release scripts can hide failures, the nested harness module is absent from
root CI, public artifacts lack a complete verification chain, and several core
files have accumulated too many responsibilities. These defects make green
output less trustworthy and future changes unnecessarily expensive.

## Acceptance

- Every release build and checksum failure terminates the release target.
- Installer downloads verify the published checksum before execution or installation.
- Root CI tests the product and nested harness module on supported operating systems with pinned tool versions.
- Public schemas and artifact references resolve to durable versioned locations.
- CLI, evaluator, hook generation, and session handling are split by responsibility without parallel implementations or behavior drift.
- Full formatting, tidy, test, race, vet, static analysis, release, install, and artifact-verification gates pass.

## Sub-Tasks

- [x] Close release, checksum, installer, schema, and artifact trust gaps.
- [x] Add nested-module and cross-platform CI coverage with pinned tools.
- [x] Refactor complexity hotspots behind existing public behavior and tests.
- [x] Remove drift and duplicated command, adapter, and evaluation paths.
- [x] Prove release and install failure paths with negative tests.

## Notes

Approved areas: 1 Release fail-open; 2 Harness CI hole; 4 Public trust chain;
6 Complexity concentration.

The three pre-existing U1000 findings in the read-only and Stop paths were
removed when TASK 005 promoted staticcheck to a required completion gate.

Read-only comparison confirmed that Golem carries the same fail-open release
loop, ignored checksum failure, unverified installer, single-OS CI, and dead
schema URLs. The standalone product therefore owns the corrected universal
implementation. Release output is flat so every uploaded asset can be covered
by one manifest, while schema URL resolution has one code owner.

Release trust now covers fail-fast cross-compilation, exact flat artifact
inventory, checksummed public schemas, draft-until-complete publication, and an
installer that preserves the existing target on every verified negative path.
CI uses immutable action commits and runs root plus nested harness formatting,
tidy, test, vet, pinned Staticcheck, and race gates on Ubuntu, macOS, and
Windows. Runtime schema aliases, RFCs, the man page, and emitted scaffold links
no longer point at the unserved reconc.dev domain.

Responsibility splits moved hook CLI/runtime routing, workflow/session CLI,
hook generation, hook merge logic, runtime lockfile trust, and Stop handling out
of their former concentration files without introducing parallel dispatch or
evaluation paths. Stable repo-local artifact names replaced current-version
paths in the bootstrapped harness.

Final proof passed for both Go modules: formatting, `go mod tidy -diff`,
`go test ./...`, `go test -race -count=1 ./...`, `go vet ./...`, and pinned
Staticcheck v0.7.0. ShellCheck, workflow YAML parsing, the release-trust
negative suite, host build, `make release VERSION=0.6.0`, and strict artifact
verification passed. The generated manifest contained exactly twelve entries.
The release-trust suite proved empty-release rejection, checksum-tool failure,
first-build termination, corruption and extra-artifact rejection, successful
verified installation, and preservation of an existing install on checksum,
missing-entry, duplicate-entry, and execution failures.

The fresh-eyes pass found no remaining implementation gap: production function
signature sets are identical before and after the CLI, hooks, runtime, and
agent-session splits; broad and race tests prove behavior; public schema tests
match every schema property to the emitted Go types; stale versioned harness
binary references remain only in deliberate migration/audit fixtures; and
`git diff --check` is clean. Schema source paths and `$id` values are
format-versioned under `schemas/v1`; remote publication remains an explicit
future push/tag operation and was not performed by this local TASK.

## Deviations

None.
