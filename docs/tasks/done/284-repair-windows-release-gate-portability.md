# TASK 284: Repair Windows release-gate portability

## Why

The first full CI run for the `reconc-v0.9.7` candidate passed macOS, Ubuntu,
LangChain, release-trust, and CodeQL but exposed Windows-only contract errors.
POSIX mode assertions treated Windows' synthetic permission bits as authority,
private temporary binaries were not validated through the existing protected
DACL boundary, and replacement tests assumed Unix rename behavior while
Windows safely blocked replacement of opened identities.

## Acceptance

- Private temporary binaries use the platform-native private-file contract:
  owner-only mode and ownership on Unix, protected current-user-only DACL and
  ownership on Windows.
- Streaming publication tests validate the representable file-mode contract on
  each platform without weakening readonly behavior.
- Identity-race tests accept either a detected replacement or an operating-
  system refusal that leaves the original opened identity intact.
- Every returned publication identity is closed in focused fault-injection
  tests, including failure paths.
- Windows-native focused tests, the complete Windows suite, local race/static
  gates, documentation, and publication gates pass before release resumes.

## Sub-Tasks

- [x] Reproduce and classify every failed Windows package from CI evidence
- [x] Secure and validate temporary binaries through the shared private-file boundary
- [x] Make mode assertions reflect the Windows readonly contract
- [x] Correct replacement fault-injection ownership and platform expectations
- [x] Add focused cross-platform regression coverage
- [x] Update filesystem and release-verification documentation
- [x] Run local gates, push the fix, and require fresh green CI and CodeQL

## Notes

- Failed run: `32627570258`, exact commit
  `0d29d4eb82c5bc1a76e7508a14ee2fb2d79c7afc`.
- Passing jobs: macOS, Ubuntu, LangChain MCP, release trust, and CodeQL.
- Failing Windows packages: `internal/atomicfile`, `internal/bootstrap`,
  `internal/cli`, `internal/ingest`, and `internal/usercli`. The CLI failure is
  a downstream usercli lifecycle refusal, not a separate root cause.
- `internal/privatefs.SecureFile` now owns file-only private security for
  same-directory transactions without changing the executable directory.
- Bootstrap's fault-injection caller retained a deliberately returned identity
  without closing it. Production callers already consume that record for
  rollback; the focused test now closes its ownership before temp cleanup.
- Windows can block rename of an opened file or repository root. Decode and
  source-reader tests now prove either stable rejection after a successful swap
  or stable original bytes after an operating-system refusal.
- The first fix run (`32628831075`) cleared every original package failure and
  exposed one remaining `internal/usercli` post-stream assertion: it compared
  exact POSIX `0700` bits instead of the Windows readonly boundary. The release
  candidate verifier now shares the same platform-native mode contract and has
  a direct writable/readonly regression table.
- No release tag or GitHub release exists. Publication remains blocked until a
  fresh candidate has successful CI and CodeQL runs.
- Local verification passed: focused package tests and race tests, Windows
  cross-compilation, Vet, Staticcheck, the complete root and harness race gate,
  publication audit, release trust, and self-hosting. Remote CI and CodeQL are
  mandatory post-commit release conditions and remain outside source mutation.
- Final product candidate `54f872305bbfefa39126adb56c463edc7bc08cad`
  passed CI run `32630023055`, including the complete native Windows suite, and
  CodeQL run `32630023046`.

## Deviations

- TASK completion requires Windows-native CI and therefore a remotely visible
  candidate commit. The user explicitly requested commit and push, so the
  verified implementation is committed while this TASK remains active. It is
  archived only after fresh CI and CodeQL succeed; the subsequent final TASK
  state must itself pass the release gates before tagging.
