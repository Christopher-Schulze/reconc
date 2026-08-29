# TASK 459: Stream and reuse executable verification

## Why

Executable verification has two avoidable whole-binary costs. The isolated hook-verification fallback reads up to 256 MiB into one byte slice before copying, and `InspectCurrent` hashes the installed target again through its PATH-resolved spelling even when both paths identify the same file.

## Acceptance

- Cross-filesystem verification copying uses bounded streaming memory and preserves source type, size, identity, digest, exclusive-target, mode, cleanup, and close-error guarantees.
- `InspectCurrent` reuses a verified target digest when the PATH-resolved executable has the same opened identity, without trusting string path equality or stale metadata.
- Behavior remains correct when source or target identity changes during copy or hashing.
- Benchmarks or allocation tests demonstrate constant copy-buffer memory and one full-content hash for a target that is also PATH-resolved.

## Sub-Tasks

- [ ] Inventory executable copy and checksum signatures plus all trust and cleanup invariants.
- [ ] Stream the hard-link fallback through a fixed buffer while hashing and revalidating the source snapshot.
- [ ] Cache and reuse installed-target digest only after same-file identity proof.
- [ ] Add deterministic identity-swap, short-write, close-failure, and same-target regressions.
- [ ] Add allocation and bytes-read evidence for large executable paths.
- [ ] Run focused hook-verification and user-CLI lifecycle tests.

## Notes

- Verified from worker findings 715 and 791 against current source.
- `linkOrCopyVerificationExecutable` uses `boundedio.ReadRegularFile(..., 256<<20)` after hard-link failure.
- `InspectCurrent` hashes `target`, resolves PATH, proves the resolved path identifies `target`, and then hashes that same executable again instead of reusing the first digest.

## Deviations
