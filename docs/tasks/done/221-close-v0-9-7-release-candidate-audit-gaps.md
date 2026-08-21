# TASK 221: Close v0.9.7 release-candidate audit gaps

## Why

The v0.9.7 source candidate passes its existing repository and release gates,
but a source-first audit found three gaps those gates did not reject: public
surfaces describe the unpublished candidate as a release, the policy-lock v6
compatibility URL is classified as a misbound tag instead of a prior immutable
publication, and same-process audit append serialization retains one mutex per
repository forever while imposing no local wait bound. The completed-task
control plane also retains stale deferred-gate notes and more than the required
ten visible Done entries.

## Acceptance

- Every public surface distinguishes the local v0.9.7 source candidate from an
  immutable published release; historical TASK ownership is accurate.
- The v0.9.6 policy-lock v6 compatibility identity has an explicit validated
  prior-publication reason and remains accepted only as input compatibility.
- Same-process audit append serialization has a finite wait, honors
  cancellation internally, releases idle per-directory state, and leaves the
  authoritative cross-process file-lock deadline unchanged.
- Focused tests prove audit gate acquisition, timeout, cancellation, recovery,
  cleanup, and concurrent append integrity; schema tests prove the exact alias
  reason.
- Completed TASK gate notes and checkboxes match fresh verification evidence,
  and `docs/tasks.md` exposes only the ten newest completed TASKs.
- Targeted fuzzing and benchmarks, repository tests, race tests, release build,
  publication audit, and release verification pass from the final source tree.

## Sub-Tasks

- [x] Audit version, publication, schema, TASK, and concurrency truth surfaces
- [x] Replace the unbounded audit append mutex registry with a bounded lifecycle gate
- [x] Correct schema alias semantics and enforce them in tests
- [x] Reconcile public documentation, release notes, publication assertions, and archived TASK evidence
- [x] Run focused fuzz, benchmark, test, race, release, and final Git verification

## Notes

- The remote `reconc-v0.9.7` tag and release are absent; v0.9.6 remains the
  latest published release. Local v0.9.7 wording must therefore remain
  candidate-only until the protected tag-bound workflow publishes it.
- The existing cross-process file lock remains the correctness authority. The
  local gate exists only to serialize goroutines before lock polling and must
  not become an unbounded wait or a permanent repository-keyed allocation.
- TASKs 206 through 210 now record their completed aggregate gates; TASKs 211
  through 216 and 219 now cite the fresh TASK 221 verification instead of stale
  queue-completion or TASK 220 blocker wording.
- All 53 discovered root-module fuzz targets pass with 500 executions each;
  the portable template currently defines no fuzz target. Targeted benchmarks
  cover every optimization benchmark added since v0.9.6 plus audit append with
  zero and 200 retained records.
- Staticcheck v0.7.0 under the module's Go 1.26.7 toolchain exposed and now
  rejects optimization-orphaned wrappers plus one overwritten bootstrap stat
  result. Root and portable-template lint and vet pass after the cleanup. The
  initial offline hook-verification failure was traced to Codex's reduced PATH;
  the exact failed test passes with the installed Bun 1.4.0 on PATH.
- Final verification passed both module `go mod tidy -diff` checks, all root and
  portable-template tests under the race detector, publication audit over 1,249
  tracked files, 211 post-boundary commits, and 3,293 post-boundary blobs, the
  canonical harness-pack check, release trust, and self-hosting. The complete
  five-target `make release VERSION=0.9.7` matrix produced and verified its
  manifest, checksums, SBOMs, notices, schema assets, completion scripts, and
  manpage.
- A fresh remote check found no `reconc-v0.9.7` tag or GitHub Release. The
  latest published release remains non-draft, non-prerelease v0.9.6. No tag,
  release, push, or other remote mutation occurred.

## Deviations

None.
