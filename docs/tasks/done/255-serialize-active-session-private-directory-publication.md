# TASK 255: Serialize active-session private directory publication

## Why

Native Windows candidate run `32552949608` passed the expanded focused
preflight, then exposed a load-dependent first-publication race in the full
suite. Concurrent active-session writers could observe the new hash-keyed
project directory after `Mkdir` but before its protected Windows DACL was
published. The observer correctly failed closed with `private Windows DACL is
not protected`, but legitimate first use must serialize that private-boundary
transition instead of exposing an incomplete directory.

## Acceptance

- Missing project-state directories are created and secured under the existing
  cross-process project-root retention lock.
- Existing unsafe directories still fail closed except for Reconc's existing
  explicit final-directory repair contract.
- The publication lock is never held while acquiring a session or
  active-pointer lock, preserving the lock order and avoiding re-entry.
- A deterministic regression proves initial active-session publication waits
  for the retention lock; the concurrent-writer regression proves the final
  private directory and pointer remain valid.
- Focused local tests, formatting, Vet, Staticcheck, the root race suite, the
  shortened native Windows preflight, full Windows tests, CLI smoke, installer,
  and cross-platform gates pass.

## Sub-Tasks

- [x] Route missing project-state directory publication through the existing
  project-root retention lock
- [x] Add deterministic lock-order and concurrent-publication regressions
- [x] Extend the shortened native Windows preflight
- [x] Run local focused and repository verification
- [x] Prove the preflight and complete suite on native Windows
- [x] Archive the TASK and resume TASK 254

## Notes

- CI run `32552949608` at source `579ac444` passed macOS, Linux including
  race, release trust, LangChain, and the expanded native Windows preflight.
  The full Windows suite failed only
  `TestActiveSessionConcurrentWritersShareOnePointerLock` while securing the
  new `projects/<digest>` directory.
- This is the same transition class previously fixed for Action State in TASK
  170: directory creation and Windows DACL publication must be one serialized
  cross-process operation. Retrying an unprotected DACL would blur a real
  unsafe-directory failure into a transient condition and is rejected.
- The deterministic retention-lock regression failed against the prior
  implementation in 40 ms because first publication bypassed the held lock. It
  passes after the production change, together with the 24-writer regression
  under the race detector and the complete shortened preflight selection.
- Local completion proof passed `make test` including formatting, publication
  audit, root and harness race suites, and release trust; `make vet`, pinned
  Staticcheck, Bash syntax, and typed TASK validation also pass.
- Candidate CI `32554431282` passed the 45-second native Windows preflight, the
  complete Windows suite, CLI build/smoke, native PowerShell installer, macOS,
  Linux race, LangChain, and release trust at source `96b6d67e`. CodeQL run
  `32554431244` passed against the same source.

## Deviations

None.
