# TASK 002: Read-only runtime truth

## Why

Inspection and evaluation commands currently compile stale policy implicitly,
so commands presented as read-only can modify repository state. Golem already
proved the explicit-refresh direction, while standalone has the stronger
atomic lockfile writer that must be retained.

## Acceptance

- `refresh` explicitly compiles policy and publishes the lockfile atomically.
- `status`, `check`, `ci`, `assert`, `can`, `diff`, `doctor`, `verify`, `session-briefing`, and TUI inspection paths never compile or write policy implicitly.
- Missing or stale lockfiles fail closed with one exact `reconc refresh <repo>` remediation.
- Help, completion, command docs, tests, and agent guidance expose the same command surface.
- Audit and briefing counts describe their actual time window and current blocking state.
- Relevant unit, integration, race, vet, and build checks pass.

## Sub-Tasks

- [x] Trace every compile and freshness call from public command entry points.
- [x] Introduce the explicit refresh contract without weakening atomic publication.
- [x] Convert inspection and evaluation paths to strict read-only lockfile loading.
- [x] Repair metrics, help, completion, docs, and tests as one public contract.
- [x] Run the full verification set and archive the TASK.

## Notes

Approved areas: 3 Read-only contract break; 5 CLI drift. Golem commit
`45d70e919` is a reference, not a patch source.

Implemented `refresh` as an explicit alias over the atomic compiler pipeline,
removed runtime and CLI auto-compilation, centralized strict lockfile
validation for diagnostics, repaired schema/source-count checks, added
truthful latest and time-windowed audit metrics, and registered the previously
missing `runloop` completion surface.

Proof: root and nested harness `go test`, `go test -race -count=1`, `go vet`,
and `go build` all pass. Live standalone `reconc status .` reports 5 rules, 4
sources, and a fresh lockfile.

## Deviations

None.
