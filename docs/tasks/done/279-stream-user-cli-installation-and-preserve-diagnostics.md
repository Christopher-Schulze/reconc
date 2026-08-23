# TASK 279: Stream user CLI installation and preserve diagnostics

## Why

User CLI install/update can retain the old binary, new binary, and downloaded
candidate simultaneously as byte slices, with a 256 MiB per-binary cap. On an
8 GiB machine this can create avoidable 512-768 MiB peaks. Status also swallows
target checksum errors and converts them into `Current=false`/`Ready=false`,
which produces misleading reinstall advice. One unreadable PATH candidate
aborts the complete diagnostic instead of reporting the usable candidates plus
an explicit warning. Receipt read locking may run its operation twice when a
lock appears between two probes.

## Acceptance

- Local install, online update, backup, checksum verification, provenance
  verification, atomic publication, and rollback stream through bounded files
  and fixed-size buffers. No path retains complete old and new binaries in
  memory simultaneously.
- Candidate and backup files are private, non-symlink, identity-checked,
  fsynced, mode-correct, and cleaned on every success/error/cancellation path.
  Rollback restores the exact prior bytes/mode or reports a recoverable explicit
  failure without deleting the only valid copy.
- Expected size and SHA-256 are verified while streaming and revalidated after
  publication. Release manifest, checksum inventory, embedded build identity,
  channel, GOOS, and GOARCH contracts remain unchanged.
- `InspectCurrent` returns checksum/read errors as structured diagnostics; it
  does not label an unreadable binary as merely outdated.
- PATH inspection preserves shell resolution order, reports unreadable/broken
  entries separately, and still identifies a later usable target when the
  platform's actual resolution semantics permit it.
- Receipt read operations execute exactly once. A lock appearing after an
  unlocked read causes result revalidation, not a second side-effecting callback.
- Peak-RSS tests or benchmarks cover near-limit local and HTTP candidates,
  existing-target rollback, cancellation, hash mismatch, and short writes.
- Installer/status docs, release scripts, platform tests, race tests, and
  complete gates pass.

## Sub-Tasks

- [x] Specify the bounded streaming candidate, backup, publication, and rollback state machine
- [x] Add streaming download/local-copy with simultaneous size and digest verification
- [x] Replace in-memory binary backup with a private identity-checked backup file
- [x] Publish and verify candidates atomically without full-byte materialization
- [x] Preserve checksum and PATH inspection errors as structured diagnostics
- [x] Make receipt read-lock acquisition single-execution and race-safe
- [x] Add cancellation, short-write, corruption, rollback, PATH, and near-limit tests
- [x] Measure peak RSS and allocation behavior
- [x] Update user CLI install/update/recovery documentation and scripts
- [x] Run platform, release-trust, race, publication, and complete gates

## Notes

- Current evidence: `installCurrent` calls `captureBinaryBackup` and
  `readBoundedBinary`; `materializeCandidate` reads/downloads the complete
  release asset before hashing and writing it.
- Current evidence: `InspectCurrent` ignores `fileSHA256` errors for installed
  and PATH-visible targets.
- Current evidence: `withReceiptReadLock` executes `operation` before checking
  whether the lock appeared, then falls through and executes it again under a
  read lock.
- PATH error handling must mirror actual OS executable resolution. Do not skip
  a first entry if the shell itself would stop/fail there; test platform
  semantics before changing the result.
- The completed transaction uses 128 KiB copy buffers for source installation,
  local and HTTP candidates, private backup, publication, checksum, and
  rollback. Full-binary byte slices were removed from production install and
  update paths.
- Near-limit 256 MiB benchmark evidence on darwin/arm64 Apple M1:
  `BenchmarkCopyReleaseCandidateNearLimitLocal` 406.3 ms, 164,560 B/op, 17
  allocs/op; HTTP 408.5 ms, 166,928 B/op, 36 allocs/op.
- Native installer scripts already download candidates to bounded files and
  required no behavioral edit. `make test-release-trust` exercised their real
  artifact, tamper, install, update, and rollback contracts successfully.
- Verification passed: focused unit tests, focused race tests, Windows and
  Linux amd64 test-binary compilation, `go vet`, pinned Staticcheck,
  `make test-fast`, reference generation checks, publication audit, harness
  pack verification, and release trust.

## Deviations

None.
