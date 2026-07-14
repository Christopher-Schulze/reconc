# TASK 010: Self-hosting and final proof

## Why

The standalone repository must prove its own product instead of relying on
claims, Golem-only wiring, or tests that never exercise a bootstrapped consumer.
The final upgrade needs one clean end-to-end truth pass across behavior,
performance, storage, hooks, bootstrap, policy, docs, and release artifacts.

## Acceptance

- This repository is bootstrapped by its own released-layout tooling without external source-tree dependencies.
- Its committed policy and TASK control plane exercise the universal gates and adapters used by new repositories.
- A clean temporary repository completes bootstrap, refresh, checks, TASK lifecycle, hook smoke tests, state pruning, and release-layout binary resolution.
- Benchmarks prove bounded Stop latency, bounded context output, bounded persistent state, and no unbounded temp/log growth.
- All product and harness checks pass from clean caches and concurrent runs.
- Documentation, `BOOTSTRAP.md`, help, completion, examples, and release metadata match verified behavior.

## Sub-Tasks

- [x] Bootstrap Reconc into itself and resolve every self-hosting finding.
- [x] Run the clean-repository golden path across all supported profiles and hook platforms.
- [x] Run adversarial, concurrent, race, scale, storage, and performance proof suites.
- [x] Reconcile all docs and remove generated verification residue.
- [x] Perform the final reality check, archive the TASK, and produce release-ready proof.

## Notes

Approved areas: 7 Adapt/merge evolved Golem generically; 22 Standalone self-hosting.
This TASK also verifies every earlier acceptance contract.

The first self-hosting plan exposed a mature-repository gap: governed bootstrap
correctly refused to overwrite the existing policy, agent contract, docs, TASK
plane, and Git hook, but therefore could not install any missing adapters. The
new `existing` profile owns only hooks, the repo-local wrapper, and an optional
stable binary. It requires a fresh compiled policy and leaves every existing
control-plane file byte-identical. Reconc now self-hosts all nine supported hook
platforms through that profile. The prior generated Git hook was stale because
it hard-coded `reconc-0.6.0`; generator synchronization replaced it with stable
or unambiguous versioned binary resolution. The source `bin/hook` remains the
canonical generator golden file, while `tools/reconc/bin/hook` is the verified
installed artifact. Local binaries under `tools/reconc/dist/` are ignored.

Self-hosting also exposed a false static conflict between the documented
`default` plus `strict` composition: one forbidden command appeared among
several valid `require_command` alternatives. Conflict analysis now reports a
contradiction only when trigger scopes overlap and one forbid rule blocks every
required alternative. The repository compiles 15 rules from six packs with no
static conflict. The repeatable `scripts/tests/self-hosting.sh` proof covers all
three bootstrap profiles, nine hook platforms, stable binary resolution,
policy health, transactional TASK block/resume/archive, retention, and cleanup;
it is wired into `make self-host` and non-Windows CI.

Apple M1 proof measurements with fixed iteration counts: Stop policy
fingerprint 16.34 ms clean and 28.85 ms with an untracked directory; Stop
policy check 16.00 ms cold, 15.38 ms warm, and 15.11 ms reentrant-clean;
deduplicated session mutation 94.47 microseconds; not-due retention 24.09
microseconds; runtime event lookup 11.25 ns with zero allocations; typical
audit record 386 bytes against the 32 KiB hard cap. Product and harness race
suites passed concurrently from separate empty `GOCACHE` directories. Scale
proofs covered 5,000-entry TASK histories, 1,000-file native assurance,
cross-process workflow cache publication, bounded JSONL concurrency, session
evidence overflow, compaction context, and 1,800-byte session briefing output.

The final storage audit found 440,169,551 bytes of abandoned Golem proof-exec
scratch state: a 425 MiB repository clone, a 26 MiB Go cache, and two empty
failed-start directories. No process held any file in those trees. Normal
proof-exec cleanup is deferred and therefore cannot run after a hard kill; the
24-hour universal grace retained the residue for roughly 20 hours. The default
grace is now two inactive hours, four times the proof command's 30-minute hard
timeout. A failable retention test proves a three-hour tree is removed while a
90-minute tree survives. The CLI prune test now redirects `TMPDIR`, `TMP`, and
`TEMP` to one test-owned root so Unix and Windows verification cannot scan a
developer's real temp directory. Post-change inspection found no remaining
`reconc-proof-*` trees; the root volume retained 16 GiB free.

The final Git status exposed one self-hosting-only ignore drift: the universal
governed renderer and scaffold already ignored `.reconc/runloop/`, but this
mature repository's manually adopted `.gitignore` did not. Both root and
nested runtime patterns now match the product contract, the self-hosting proof
asserts that contract directly, and generated runloop locks no longer appear as
untracked source files.

The first staged policy check then exposed a strict-gate semantic bug: colocated
`internal/**/*_test.go` writes matched both the broad source scope and the
required companion scope, so the evaluator discarded the real test evidence.
`couple_change` now classifies overlapping `when_paths` as companions before it
evaluates primary writes. Failable tests prove source-only blocks, source plus a
colocated test passes, and a test-only change does not recursively require
another test.

Running the golden path against a dirty outer worktree exposed one final test
illusion: it executed the temporary repository's pre-commit file from the outer
repository working directory, so Git resolved the wrong root. The proof now
stages the governed fixture, executes the hook from that fixture root, and
asserts that the hook report names the governed repository.

The real pre-commit proof still emitted long false read warnings because `ci`
inherited active commands, command results, and claims but dropped recorded
read paths. Active session evidence now carries reads through the same bounded
state channel. The CLI integration test proves staged CI consumes both a prior
architecture read and a successful test command.

Final proof: `make self-host` passed; product and template-harness `go test`,
`go vet`, Staticcheck v0.7.0, and race suites passed from separate empty
`GOCACHE` directories; Shellcheck, Bash syntax, JSON, YAML, Bun adapter, and
wrapper-identity checks passed. Release generation and checksum verification
produced exactly five binaries, three completions, one man page, three schemas,
and `SHA256SUMS`, then cleanup left no release residue. Deep doctor returned six
OK checks, all nine hook platforms were active, the TASK board was valid, and
the existing-profile plan reported all twelve installed artifacts unchanged.
Dry-run retention reported 38,482 bytes of repo runtime and zero external state
or owned temp bytes.

## Deviations

None.
