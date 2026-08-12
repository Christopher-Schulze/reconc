# TASK 166: Restore native Windows release truth

## Why

The first real `v0.9.6` publication run exposed deterministic failures in the
native Windows CI that local cross-compilation could not execute. The private
Action Plane DACL validator rejected a Windows-normalized owner-only ACL, test
fixtures supplied inherited temporary roots where production correctly requires
private state, and an unlocked active-session probe could transiently prevent
atomic Windows replacement. The release workflow could publish after macOS
cross-compilation without waiting for native Windows execution.

Christopher explicitly authorized repairing these defects, keeping source and
release version `0.9.6`, pushing the corrected source, and replacing the
published `reconc-v0.9.6` release with artifacts from the final verified commit.

## Acceptance

- A protected Windows DACL is accepted only when every ACE grants access solely
  to the current owner and the combined ACEs provide full access to the object
  plus inheritable full access to directory children; unknown identities, deny
  entries, missing coverage, unprotected DACLs, foreign ownership, reparse
  points, and malformed ACLs still fail closed.
- Native Windows tests prove both the Windows-normalized multi-ACE owner-only
  form and rejection of every access-widening or coverage-losing mutation.
- Cross-platform MCP gateway fixtures create Reconc-owned private homes instead
  of relying on platform-specific temporary-directory ACLs.
- Active-session reads and writes share one lock before opening the pointer, so
  concurrent sessions cannot cause an internal Windows sharing violation or
  lose/cross state.
- The canonical release workflow cannot publish until native Windows root and
  template tests, Windows binary smoke tests, and the native Windows installer
  gate pass on the exact release tag.
- `make test`, race suites, vet, Staticcheck, coverage, fuzz, release-trust,
  self-host, ShellCheck, module integrity/tidy, vulnerability checks, Windows
  cross-compilation, and native Windows CI pass on the final snapshot.
- Source version remains exactly `0.9.6`. Main, the annotated
  `reconc-v0.9.6` tag, release notes, uploaded inventory, checksums, and every
  provenance subject resolve to the same final commit and verified bytes.

## Sub-Tasks

- [x] Reconstruct both failing Windows runs and map every failure to its exact
      production or fixture root cause
- [x] Replace the single-ACE assumption with a complete owner-only effective
      and inherited DACL proof
- [x] Add native Windows positive and adversarial ACL regression coverage
- [x] Bind every Action Ledger live, archive, lock, journal, and backup path to
      the same cross-platform private-filesystem contract
- [x] Preserve pre-security journal recovery identity and keep archive fuzzing
      bound to real private storage
- [x] Make the bounded fuzz runner deterministic under constrained local worker
      shutdown
- [x] Make all affected gateway fixtures request a newly created private home
- [x] Remove the unlocked active-session read/write race and strengthen its
      concurrent regression
- [x] Make native Windows execution a prerequisite of release publication
- [x] Decouple the oversized-result containment regression from the unrelated
      short call-timeout path exposed by the complete race gate
- [x] Make invalid tool-refresh lifecycle coverage deterministic for both safe
      orderings: response delivery before closure or redacted fail-closed
      interruption before delivery
- [x] Reconcile release notes, documentation, and TASK truth
- [x] Run all local gates and inspect the complete diff
- [x] Commit once, push main, and prove every native Windows check green
- [x] Move the authorized `reconc-v0.9.6` annotated tag to the final commit and
      replace the release through the canonical workflow
- [x] Download and independently verify every published asset, checksum,
      manifest, notice, release note, tag binding, and provenance attestation

## Notes

The failing push and tag runs were GitHub Actions `31588700419` and
`31588719127`. Both failed the native Windows job with the same DACL classes;
the main push additionally reproduced one `MoveFileExW: Access is denied`
during concurrent active-session publication. The macOS release workflow run
`31588746396` passed because it cross-compiled Windows but did not execute the
native Windows job before publishing.

The first complete local race rerun additionally proved that the oversized
downstream-result test's five-second call timeout could win under instrumentation
and test `deadline_exceeded` instead of the intended bounded-result failure. The
fixture now preserves the production default's headroom for that containment
path without weakening either assertion.

The second complete race rerun exposed a second valid ordering: the fatal
invalid-catalog refresh can close the upstream session immediately before the
triggering response is delivered. The regression now accepts either completed
delivery or that exact redacted fatal error, while always proving downstream
execution, closed admission, and omission of the injected catalog text.

The first candidate Windows run caught one incorrect reading of the dependency
contract: `SECURITY_DESCRIPTOR.DACL` returns whether the ACL was defaulted, not
whether it is present. Presence is already represented by its error result. The
validator and its native regression now use that exact signature.

The second candidate run, `31597343598`, confirmed the normalized DACL fix and
then exposed the remaining platform truth: four tests asserted POSIX modes or
used non-private Windows fixtures, one executable-replacement fixture omitted
the required `.exe` suffix, and Action Ledger JSONL paths did not yet invoke the
Windows DACL validator. The fixtures now test their intended behavior on each
platform, while the production JSONL layout secures every new private path and
rejects existing permission or ACL drift without repair. Cross-platform tests
prove the layout-security calls and native Windows adversarial tests widen both
live-file and lock DACLs to verify fail-closed behavior and non-mutation.

The third candidate run, `31610907729`, passed every preceding native Windows
package and isolated one final platform-specific fixture error: Windows denies
replacement of a mapped running executable before Reconc can observe drift.
The regression now proves that native host protection, then stops the child,
performs the replacement, and invokes the same production dispatch-boundary
resampling path to prove Reconc rejects the changed executable identity too.

The final source review caught two secondary regressions before publication: a
trailing empty hash field made the first legacy-journal compatibility attempt
non-compatible, and the malformed-archive fuzzer's zero-value storage stopped
inputs before they reached archive and decoder logic. The legacy identity now
uses the exact prior field sequence with an independent regression fixture, and
the fuzzer exercises a real private project storage capability.

The complete fuzz gate then reproduced Go issue 75804 under Go 1.26.5: no
crashing input was written, each target passed in isolation, and time-bounded
runs failed randomly with `context deadline exceeded` under both eight workers
and one. The runner now defaults to 500 exact executions with one worker per
target and ten exact executions per minimization attempt, retaining explicit
budget and parallelism overrides without relying on the affected deadline race
or the default 60-second minimization window. A disposable per-run Go fuzz cache
also makes the fixed gate independent of the machine's accumulated corpus while
still writing any crashing input into the repository's standard testdata path.

The final local snapshot passed `make test`, both complete race
modules, vet, pinned Staticcheck, ShellCheck, both module verification and tidy
diffs, all 50 registered fuzz targets, whole-module root and portable-template
coverage measurement, both Govulncheck modules, the pinned external LangChain
proof, self-hosting, the complete five-target `v0.9.6` release build, and exact
release verification. Windows-native candidate and tag gates plus final remote
asset and provenance verification are mandatory post-commit publication gates
for this same snapshot.

## Deviations

Replacing an already published tag and release normally violates the immutable
release rule. Christopher explicitly authorized this one replacement so the
public `v0.9.6` identity points only to the corrected, fully verified build.
The TASK is archived in the release-candidate commit because protected native
Windows checks, tag movement, release replacement, and remote byte/provenance
verification necessarily occur against that immutable committed snapshot; any
failure blocks `main` or publication and reopens the TASK instead of being
reported as complete.
