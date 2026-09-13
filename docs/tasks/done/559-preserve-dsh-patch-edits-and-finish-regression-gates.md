# TASK 559: Preserve DSH patch edits and finish regression gates

## Why

Audit finding 7 reproduces silent loss of a modified managed DSH patch during reinstall. Findings 8-9 require complete regression and documentation propagation across the finished repair.

## Acceptance

- Reinstall refuses a drifted activation patch before changing any managed artifact, including with force; unchanged reinstall and foreign-patch refusal remain correct.
- Tests prove exact byte preservation and lack of collateral mutation for install, sync/refresh where applicable, and uninstall.
- All nine audit findings are mapped to completed implementation, tests, or precise documented support boundaries.
- Full project gates, build, isolated self-host checks, and documentation/publication checks pass; commits are pushed to origin/main without a release.

## Sub-Tasks

- [x] Tighten patch ownership preflight and add preservation regressions.
- [x] Re-read changed code/docs/tests and reconcile every audit finding.
- [x] Run final gates, archive the task, commit, push, and verify remote state.

## Notes

The exact generated patch is stable; a matching header alone does not prove ownership of modified content. User-specific overlays belong in a separate patch file. Preserve current managed artifacts on every conflict. Source/offline acceptance is sufficient; no DSH host/model run belongs to these tasks.

Findings 1-3 are closed by TASK 557's native execution/provider guards and documented supported alternatives. Findings 4-6 are closed by TASK 558's process-bound callbacks, cancellable bounded queue, and compact observations. Finding 7 is closed by exact-content patch preflight with byte-preservation regressions for reinstall, force, scaffold refresh, uninstall, and transactional repository sync. Finding 8 is covered by all three tasks' durable generated-extension, real policy/worker, and lifecycle/ownership regressions. Finding 9 is closed by canonical documentation and portable skill references defining source/offline acceptance and the same support boundaries as the implementation.

Targeted DSH hook/bootstrap tests and the portable skill validator passed. Source, tests, and changed documentation were re-read before the final uncached gates; no source/artifact edits are made while those gates run.

TASK 558's hosted CI exposed a Bun 1.3.14 `Buffer.byteLength` undercount for an unpaired surrogate followed by mixed Unicode (code unit 55370: 14 reported input bytes versus 15 encoded bytes; JSON frame 53 versus 54). This did not reproduce on local Bun 1.4.2 or Node. The final local gate was stopped before edits. The counter now measures JSON/UTF-8 directly from code units with bounded work, and tests use actual TextEncoder bytes as the oracle. The original failing case passes with the temporary CI-pinned Bun binary; the global Bun and DSH installations are untouched. Scaffold and pack carry the same correction.

## Deviations

The final gate uses temporary Bun 1.3.14 to match hosted CI, without replacing the user's global runtime. The pack correction was reviewed as a temporary candidate, ZIP-checked, and matched byte for byte after propagation.

Final validation passed: `make test TEST_PARALLELISM=4` (publication/reference/pack audits, uncached root and template race suites, release trust), `make build vet lint self-host`, targeted DSH/bootstrap/scaffold tests, portable skill validation, and `git diff --check`. All gates used the consolidated source and generated artifacts. No DSH installation, agent/model run, product tag, or product release was performed.
