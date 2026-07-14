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

- [ ] Bootstrap Reconc into itself and resolve every self-hosting finding.
- [ ] Run the clean-repository golden path across all supported profiles and hook platforms.
- [ ] Run adversarial, concurrent, race, scale, storage, and performance proof suites.
- [ ] Reconcile all docs and remove generated verification residue.
- [ ] Perform the final reality check, archive the TASK, and produce release-ready proof.

## Notes

Approved areas: 7 Adapt/merge evolved Golem generically; 22 Standalone self-hosting.
This TASK also verifies every earlier acceptance contract.

## Deviations

None.
