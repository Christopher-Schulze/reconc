# TASK 240: Verify external audit findings

## Why

An external OMP audit reported 106 findings while TASKs 227 through 239 were
still landing. Every claim therefore needs current-source verification before
it can influence implementation. The audit must separate real open defects
from findings already fixed by later commits, deliberate contracts, unreachable
states, and claims whose severity depends on an incorrect model of the code.

## Acceptance

- Every finding from F-1 through F-106 is classified against current `main`.
- Open findings cite the implementation TASK that owns their remediation.
- Fixed findings name the current mechanism that closes them.
- Rejected findings state the exact contract or source fact that invalidates
  them; no finding is rejected only because it is inconvenient to fix.
- The queue preserves macOS as the primary development platform, Linux as a
  first-class side platform, and bounded Windows compatibility without making
  Windows the local development bottleneck.
- Go 1.27 opportunities are attached only where the installed standard library
  provides a concrete improvement: `os.Root.Chmod`, `testing/synctest`, and
  streaming `encoding/json/jsontext` decoding.

## Sub-Tasks

- [x] Parse the complete attached OMP transcript and normalize F-1 through F-106
- [x] Compare every claim with current source, tests, completed TASKs, and Git history
- [x] Recheck the live failed Windows CI run and its elapsed feedback path
- [x] Group the 71 open findings into implementation-sized TASKs
- [x] Record stale and rejected claims so they cannot silently re-enter the queue
- [x] Verify queue numbering, detail links, and documentation consistency

## Notes

Current classification: 71 open findings, eight stale or already fixed
findings, and 27 rejected findings. `Open` means the source claim is real and
has a measurable correction, not that the external severity was accepted.

| Finding | Classification | Current evidence or owner |
|---|---|---|
| F-1 | Open | TASK 243: `lockdiff.indexRules` still rejects legal `observe` and `fix` modes. |
| F-2 | Open | TASK 243: `require_read` duplicate detection still selects absent `when_paths` instead of its required path contract. |
| F-3 | Open | TASK 244: trailing redirect removal still tokenizes with quote-blind `strings.Fields`. |
| F-4 | Open | TASK 243: non-config documents still accept and ignore `default_mode`. |
| F-5 | Open | TASK 243: `optionalContainList` still accepts whitespace-only entries. |
| F-6 | Fixed | TASK 227 removed the production-dead regular-expression inline parser. |
| F-7 | Fixed/intentional | TASKs 191 and 232 bound glob expansion, tolerate disappearance, and cache the parsed recipe; content hashing remains the deliberate anti-spoof freshness proof. |
| F-8 | Open | TASK 245: generated-hook ownership still uses substring markers in multiple installers. |
| F-9 | Fixed | TASK 230 removed warning-text matching from compiled-lockfile discovery. |
| F-10 | Open | TASK 243: every migration and the migration driver still recompute the same lock digest. |
| F-11 | Fixed | TASK 236 corrected the stale compiler comment. |
| F-12 | Open | TASK 253: the policy-fragment warning return channel is still structurally empty. |
| F-13 | Fixed | TASK 237 replaced the insertion sort with `sort.Strings`. |
| F-14 | Open | TASK 244: batch script exit `2` can still become pass when parsed failure lists are empty. |
| F-15 | Open | TASK 244: the pre-decision cache key still omits live repository Git aliases consulted by shell policy. |
| F-16 | Open | TASK 244: `stopPolicyScanCache.stable` still returns true for an empty lock hash despite its fail-closed contract. |
| F-17 | Open | TASK 245: generated OMP commands still use `sh -lc`, so login-profile output can poison strict decision JSON. |
| F-18 | Open | TASK 246: installer provenance verification remains opt-in and silently continues by default. |
| F-19 | Open | TASK 246: first-install `install-cli` failure still exits successfully after binary publication. |
| F-20 | Open | TASK 247: task claims still execute a repo-local binary without freshness or provenance binding. |
| F-21 | Open | TASK 247: generated-reference audit still hardcodes the `codebase/` layout. |
| F-22 | Open | TASK 245: ZCode merge still overwrites an explicit user `hooks.enabled: false`. |
| F-23 | Open | TASK 244: evidence snapshots still stringify the full platform stat object instead of stable file identity fields. |
| F-24 | Open | TASK 245: policy scripts receive `HOME` while documentation describes secret minimization without declaring the trusted-script boundary. |
| F-25 | Open | TASK 253: runtime still carries a hand-written integer formatter. |
| F-26 | Open | TASK 253: CLI still carries a second hand-written integer formatter despite already importing `strconv`. |
| F-27 | Open | TASK 244: PreToolUse payloads are still parsed repeatedly across key, path, and handler calls. |
| F-28 | Rejected | OMP and Pi adapters intentionally encode different abort and host-result contracts; textual similarity alone does not justify a shared template. |
| F-29 | Rejected | the measured command-result type is fully JSON-serializable and earlier mutation paths already mark serialization failure; the claimed budget bypass is unreachable. |
| F-30 | Open | TASK 253: three production helpers remain used only by tests or benchmarks. |
| F-31 | Open | TASK 246: `.PHONY` still advertises the absent `release-all` target. |
| F-32 | Open | TASK 247: harness pruning hashes the unresolved repository path while audit JSONL uses the resolved identity. |
| F-33 | Rejected | replacement modules are representable in the SBOM but deliberately rejected by the notices/release gate, so the overall release remains fail closed. |
| F-34 | Open | TASK 247: stack placeholder expansion still replaces every literal `project` substring. |
| F-35 | Rejected | `manifest` is a list-only release asset selector; its output is assembled by the release manifest stage, not the generated-asset writer. |
| F-36 | Open | TASK 246: release asset copy still has a destination check/copy race against its no-clobber promise. |
| F-37 | Rejected | `test -ef` is available on the supported macOS and Linux shells and no target-platform failure was reproduced; generic historical POSIX portability is outside the stated platform contract. |
| F-38 | Open | TASK 247: architecture graph mapping still searches module substrings anywhere in an import path. |
| F-39 | Open | TASK 247: added-line auditing still treats `//` inside a Go string such as a URL as a comment opener. |
| F-40 | Open | TASK 253: release-trust still authorizes unused `actions/upload-artifact`. |
| F-41 | Fixed | TASK 237 removed the duplicate prospective-path nil check. |
| F-42 | Open | TASK 252: doctor still reports oversized Grok output before the underlying execution failure. |
| F-43 | Rejected | OMP strict continuation is intentional, tested, and bounded by the documented eight-turn host continuation limit. |
| F-44 | Open | TASK 252: a selected `###` agent-guide section still consumes following `##` sections. |
| F-45 | Open | TASK 248: legacy receipt import still invents pack compatibility bounds after a digest mismatch. |
| F-46 | Rejected | `state/binding` is independently proven by equal before/after repository fingerprints; policy-proof failure does not make that pass statement false. |
| F-47 | Open | TASK 248: the user preset directory is still followed through `os.Stat` when it is a symlink. |
| F-48 | Open | TASK 248: failure to resolve the user home still falls back to a CWD-relative `.reconc`. |
| F-49 | Open | TASK 248: bootstrap and repository sync still disagree on `.git` symlink and worktree-file semantics. |
| F-50 | Rejected | recomputing the plan digest in `ValidatePlan` is the required tamper check; receipt presence does not remove that requirement. |
| F-51 | Open | TASK 248: receipt advance still degrades an unchanged `binary@version` component to unpinned `binary`. |
| F-52 | Open | TASK 248: `adopt --apply` still performs an unlocked read-merge-write of `.reconc.yml`. |
| F-53 | Open | TASK 248: removal still leaves obsolete private bootstrap receipts without a bounded retention rule. |
| F-54 | Rejected | tracked CLI output writers merge write failures into `Run`; the remaining impossible marshal failures do not justify eight noisy wrappers. |
| F-55 | Rejected | `help <command>` is correctly rewritten and dispatched. |
| F-56 | Rejected | Grok inspect output is already bounded to 4 MiB. |
| F-57 | Open | TASK 242: observer wait failures still return without cancelling the pending call or releasing `sendMu`. |
| F-58 | Fixed | TASK 239 removed the unsupported Windows read-only directory `FlushFileBuffers` call. |
| F-59 | Open | TASK 241: stack depth is still computed with the platform separator after paths were converted to slash form, disabling the cap on Windows. |
| F-60 | Open | TASK 251: atomic compare errors still return before closing the current file descriptor. |
| F-61 | Open | TASK 245: Grok child environment still appends rather than replaces `RECONC_GROK_STEER`. |
| F-62 | Rejected | Reconc models `rtk` as one transparent executable wrapper; `rtk proxy rm` executes `proxy` with arguments, not `rm` as a hidden executable. |
| F-63 | Open | TASK 245: changing reason text still resets the Grok no-progress bound without a material event. |
| F-64 | Rejected | icon payload, dimensions, total pixels, and icon count are bounded and decoding is sequential; the claimed multi-gigabyte peak allocation is not possible. |
| F-65 | Open | TASK 242: tool pages are still retained before the aggregate 8 MiB catalog bound is enforced. |
| F-66 | Open | TASK 249: a 64 MiB custom-host payload is still expanded into a full `map[string]interface{}` tree. |
| F-67 | Open | TASK 252: exported lifecycle reconstruction still ignores timestamp parse errors. |
| F-68 | Rejected | once the child deadline wins, timeout classification is correct; a later parent cancellation cannot retroactively change the completed cause. |
| F-69 | Rejected | the fixed 32-symbol alphabet divides the byte domain exactly; there is no current modulo bias. |
| F-70 | Rejected | durable receipt verification intentionally checks validity at signing time; current-time policy expiry is a separate authorization decision. |
| F-71 | Rejected | the ledger retry behavior is already encoded and tested; absence of an extra prose comment is not a product defect. |
| F-72 | Rejected | bounded tail decoding is local and deterministic; no behavioral drift or material duplicate cost was shown. |
| F-73 | Rejected | the current lock-security retry explicitly revalidates the private lock after waiting. |
| F-74 | Open | TASK 252: budget candidate construction still collapses internal errors to an empty candidate set, losing the true cause. |
| F-75 | Open | TASK 252: the credential scanner still misses compatibility-stable confusables such as small-capital `ꜱ`. |
| F-76 | Rejected | current source contains one `packReviewStale` implementation, not two. |
| F-77 | Rejected | returning the persisted replacement version together with `result_withheld` is the intentional reconciliation contract and callers retain the version. |
| F-78 | Open | TASK 250: a configured package manager with no detected manager still skips its evidence gate. |
| F-79 | Open | TASK 250: substantive proof interprets `max_age_hours: 0` as immediately stale instead of no freshness requirement. |
| F-80 | Rejected | content-scanning gates correctly skip deleted and non-regular changed paths; no content remains to scan, and symlinks to regular files are resolved and scanned. |
| F-81 | Open | TASK 251: TASK rollback still discards `safeTransactionPath` errors. |
| F-82 | Open | TASK 252: impact comparison still evaluates current and candidate policies against two sequential live-filesystem states. |
| F-83 | Open | TASK 252: command-proof index-lock retry can still accumulate forty 30-second Git timeouts and matches error text. |
| F-84 | Open | TASK 252: later diagnostic classification still overwrites a more severe stale or invalid state. |
| F-85 | Open | TASK 253: `enableCodexHooks` remains production-dead. |
| F-86 | Open | TASK 253: user-template directory empty guards remain unreachable. |
| F-87 | Rejected | a successful apply has no blocking issues to skip; the zero value is consistent with the report contract. |
| F-88 | Open | TASK 248: bootstrap command quoting still mishandles backslashes and shell interpolation. |
| F-89 | Open | TASK 248: YAML scalar quoting still misses indicator-prefixed values. |
| F-90 | Open | TASK 253: the yaml.v2 map-key normalization branch remains unreachable under yaml.v3. |
| F-91 | Rejected | Git dirty-path collection reports files, while direct detail-dir and slash-suffixed parent paths are already covered; the bare grandparent state is not produced. |
| F-92 | Open | TASK 251: explicit `done_visible: 0` is still treated as absent instead of invalid. |
| F-93 | Open | TASK 251: TASK inspection rereads the overview but does not revalidate every detail file read in the same snapshot. |
| F-94 | Fixed | current `activateDetail` parses only checklist markers inside `## Sub-Tasks`. |
| F-95 | Rejected | globally unique TASK IDs make basename reuse invalid, and an existing archive target fails loud rather than being overwritten. |
| F-96 | Open | TASK 252: TUI still discards audit and active-session observation errors. |
| F-97 | Open | TASK 253: context-size code still documents a nonexistent `--order` option. |
| F-98 | Open | TASK 252: proof redaction still replaces a common repository basename such as `go` or `docs` throughout evidence text. |
| F-99 | Rejected | command proofs enter the bundle only through `LoadCurrentSuccesses`; `fresh: true` is a guaranteed exported assertion, not computed uncertainty. |
| F-100 | Open | TASK 251: purge releases the receipt lock and then unlinks its lock file, permitting inode-split locking under contention. |
| F-101 | Rejected | selecting the attestation executable is an intentional test/offline integration point; an actor controlling both environment and PATH is outside this local process boundary. |
| F-102 | Open | TASK 250: runtime `applicable_if` evaluation can return on a literal match before validating later glob syntax. |
| F-103 | Open | TASK 250: runtime command-policy evaluation treats every non-`any` value as `all` instead of rejecting malformed compiled state. |
| F-104 | Open | TASK 250: package-script and dependency-pin JSON facts still disagree on UTF-8 BOM handling. |
| F-105 | Open | TASK 253: command suggestion initializes the best distance so the final distance-three rejection branch is unreachable. |
| F-106 | Open | TASK 253: impact manifest matching retains a length guard made unreachable by its earlier unique-entry loop. |

The failed current Windows run is GitHub Actions run `32521837351`. Its Windows
job spent about nine minutes in the unscoped full test command before exposing
the first grouped failures, while macOS, Ubuntu, LangChain MCP, and release
trust passed. TASK 241 therefore requires a focused native Windows regression
stage before the full Windows suite; cross-compilation remains compile proof,
not runtime proof.

## Deviations

None.
