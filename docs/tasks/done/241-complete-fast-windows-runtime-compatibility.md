# TASK 241: Complete fast Windows runtime compatibility

## Why

The current Windows CI job still fails after TASK 239, but it reveals failures
only after one unscoped native `go test ./...` run lasting roughly nine minutes.
Most package failures share two platform causes: read-only descriptor mode
repair and DACL protection. Four independent tests also encode invalid or
brittle Windows assumptions. Windows remains supported, but it must not become
the pacing platform for macOS-first development.

## Acceptance

- Atomic mode reconciliation uses Go 1.27 `os.Root.Chmod` relative to the
  already bound parent, then revalidates target identity; it does not weaken
  no-follow identity checks.
- Private directories, files, and locks receive and subsequently validate a
  protected current-user DACL through a Windows API path that actually persists
  `PROTECTED_DACL_SECURITY_INFORMATION`.
- Windows tests assert DACL and readonly-attribute contracts instead of Unix
  `FileMode.Perm` values that Windows cannot represent.
- Sparse-file coverage uses the native sparse-file contract without allocating
  the declared logical size on the runner.
- PowerShell remediation preserves every native argv element under the runner's
  actual Windows PowerShell version, including quotes, backslashes, newlines,
  metacharacters, and an empty trailing argument.
- Pi trust status is compared as a typed status contract, and repository-root
  replacement is checked from fresh filesystem identity rather than lazy
  Windows `os.FileInfo` state.
- Slash-normalized stack paths use slash-based depth counting on every OS and
  stop at the documented depth and entry limits.
- CI runs a focused native Windows contract stage before the full suite. A
  recurrence in the affected packages is visible within four minutes after
  module download instead of waiting for the full Windows run.
- Full Windows tests, CLI smoke, installer tests, macOS/Linux gates, Windows
  cross-compilation, and documentation pass without keeping a local Windows
  environment running.

## Sub-Tasks

- [x] Add focused failing Windows regressions for descriptor rights and DACL protection
- [x] Correct Windows atomic mode repair with Go 1.27 `os.Root.Chmod`
- [x] Apply and verify protected DACLs through the exact supported Win32 contract
- [x] Correct sparse-file, PowerShell, Pi-status, root-identity, and depth-limit tests
- [x] Add a fast native Windows preflight stage before the complete Windows suite
- [x] Run focused CI, then one complete Windows and cross-platform verification pass
- [x] Update filesystem and CI documentation with the proven platform boundaries

## Notes

- External finding: F-59. F-58 was already fixed by TASK 239.
- Current evidence is GitHub Actions run `32521837351`, Windows job
  `96895484658`; macOS, Ubuntu, LangChain MCP, and release-trust jobs passed.
- Repeated DACL failures affect audit, CLI, completion, command proof,
  proof-bundle, session, and user-install packages through shared private-state
  primitives. They are one root cause, not separate fixes.
- The first focused stage should name only packages and regression patterns that
  exercise shared Windows primitives. The complete suite remains required after
  that stage passes; fail-fast organization must not reduce coverage.
- Cross-compilation proves Windows buildability only. Runtime behavior remains
  verified by short-lived GitHub-hosted Windows jobs.
- Local verification on 2026-08-21: both workflow files parsed as YAML; all ten
  affected packages passed natively on macOS and compiled for `windows/amd64`;
  the focused remediation quoting regressions, publication audit, harness-pack
  check, complete release-trust gate, and release negative paths passed. Native
  Windows runtime behavior remains the only unproven acceptance boundary.
- Candidate run `32527605982` proved the preflight reports a shared Windows
  regression in 30 seconds. The DACL, atomic mode, sparse-file, PowerShell,
  Pi, root-identity, and depth regressions passed; the remaining failure exposed
  that Windows link-count validation was still a no-op. Private locks now read
  `NumberOfLinks` from their open handle before and after security repair.
- Candidate run `32527835477` passed that expanded preflight in 32 seconds. Its
  complete suite then isolated the remaining failures to Reconc-owned state
  directories that incorrectly used validate-only handling for inherited
  Windows ACLs. Session, receipt, command-proof, and policy-proof boundaries now
  repair only their final product-owned directory; operator-owned action-state
  roots retain strict validate-only behavior. The preflight now exercises each
  repaired owner directly.
- After that repair, all affected command-proof, policy-proof, session, receipt,
  CLI, completion-gate, and proof-bundle packages passed natively on macOS and
  compiled together for `windows/amd64`. Offline hook-verification failures now
  print the exact degraded surfaces if native Windows behavior still diverges.
- Candidate run `32529461921` passed the expanded native Windows preflight in
  49 seconds. The subsequent full run was intentionally cancelled after a final
  source review found that `RepairDirectory` reapplied an already-valid Windows
  DACL on every hot-path access and did not perform the documented final path
  identity check after named security mutation. Valid owned directories now
  return after descriptor security validation; repaired or created directories
  revalidate the final directory entry before returning.
- Candidate run `32530875733` failed only the new replacement regression: its
  test fixture used lazy path-only Windows `os.FileInfo`, whose file ID follows
  the renamed path on first `os.SameFile` use. Production uses descriptor-backed
  frozen identity. The fixture now freezes its initial file ID before replacing
  the path, matching the production contract; all product-owned DACL cases in
  that same preflight passed.
- Candidate run `32531107955` passed the complete 40-second preflight; every
  full-suite package except `internal/ingest` passed. That package retained a
  path-only lazy Windows `os.FileInfo` for `.reconc.yml`, so atomic replacement
  was not detected on first comparison. Source-load contexts now confirm the
  config twice during capture, freezing its Windows file ID before any later
  revalidation, and the regression is part of the early preflight.
- Candidate run `32532220171` passed the 38-second preflight, the full Windows
  package suite, CLI smoke, every Linux/macOS gate, release trust, and CodeQL.
  Its installer test then exposed one elevated-token boundary: Windows may make
  the Administrators group the default owner of a newly created object even
  though Reconc grants only the token user access. Private-state publication
  now opens with `WRITE_OWNER` and assigns both the current-user owner and the
  protected current-user DACL in the same named security update.
- Candidate run `32533657621` failed during preflight compilation because a
  readability-only extraction inferred the Win32 security-information mask as
  `int`. The mask now has its exact `windows.SECURITY_INFORMATION` API type;
  the canceled cross-platform jobs in that run are not verification evidence.
- Candidate run `32533856311` passed the native preflight, every package through
  `privatefs`, Linux, macOS, release trust, LangChain, and CodeQL. Its complete
  Windows suite then exposed a test-friction defect: the active-pointer lock
  regression performed 24 writers times 40 fsyncing publications and allowed a
  valid waiter to starve behind the production 30-second bound. The test keeps
  all 24 simultaneous writers and four repeated handoff generations, preserving
  the sharing-race contract without masquerading as an unbounded soak test.
- Candidate run `32534987784` passed the expanded preflight and the shortened
  active-pointer regression. Three unrelated MCP lifecycle tests then exhausted
  real 5-/10-second deadlines while the runner concurrently executed the
  roughly six-minute audit and hook packages; `mcpgateway` passed twice locally
  without modification. The complete Windows suite now limits package-binary
  concurrency to two via Go's documented `-p` build flag. Coverage is unchanged,
  but deadline-bearing packages no longer compete with every heavy package at
  once for the runner's CPU and filesystem.
- Final candidate run `32536131030` passed on source `adde5f6`: the expanded
  native Windows preflight completed in 52 seconds, the complete root/template
  suite passed with two package binaries at a time, CLI build/version/help
  smoke passed, and the native PowerShell installer passed the elevated-owner
  receipt path in three seconds. Ubuntu including race tests, macOS, release
  trust, and LangChain passed in the same run; CodeQL run `32536131332` passed
  on the identical source. The authoritative Windows suite took 22 minutes and
  4 seconds, while regressions in the repaired boundaries fail in the separate
  sub-four-minute preflight.

## Deviations

None.
