# TASK 467: Make Windows identity replacement tests deterministic

## Why

The first `reconc-v0.9.8` release run failed in the native Windows gate because an atomic-file regression test allowed the filesystem to recycle the original file identity before it created the replacement. The same commit passed the ordinary Windows CI gate, proving that the setup is nondeterministic rather than a stable product failure.

## Acceptance

- Atomic-file identity-swap tests create and verify a distinct replacement identity before removing the original path.
- Byte, stream, and direct target-validation coverage retain their adversarial replacement assertions without weakening production checks.
- Focused local tests, formatting, static gates, native Windows CI, CodeQL, and the exact-tag release workflow pass.
- The failed unpublished `reconc-v0.9.8` tag is replaced only after its corrected commit is green, and the release is independently verified.

## Sub-Tasks

- [x] Make every affected identity-swap fixture deterministic.
- [x] Run focused local verification and inspect the complete diff.
- [x] Archive, commit, push, and prove the exact remote gates.
- [x] Replace the unpublished tag, publish `reconc-v0.9.8`, and verify its artifacts and attestations.

## Notes

- Release run `33332306005` failed only `TestWriteIfCurrentRejectsConcurrentIdentityReplacement`; artifact construction and publication never started.
- The fixture renamed the original away before creating its byte-identical replacement. Windows may recycle the original file ID in that gap, causing `os.SameFile` to report the replacement as the authorized identity.
- The deterministic setup creates the replacement while the original still exists, proves the two identities differ, and only then swaps the replacement into the target path.
- All three adversarial identity-swap paths share the deterministic fixture. The focused tests passed 20 consecutive runs, the full atomic-file package passed, Windows amd64 test compilation passed, and `make vet`, `make lint`, `gofmt`, and `git diff --check` passed.
- Native Windows CI, CodeQL, exact-tag release publication, artifact checksums, and attestations are post-commit operational evidence and must all be green before final handoff.

## Deviations
