# TASK 462: Restore the Windows final gate

## Why

The final cross-platform run is green on macOS and Linux but the Windows job exposes platform-specific test-fixture security drift, open-handle deletion incompatibilities, a non-portable file-identity assertion, and excessive Git Bash process pressure in the generated Bun continuation contract. These failures obscure the production guarantees that the final gate must prove.

## Acceptance

- Private repository-run fixtures use the same protected Windows directory contract as production and continue to exercise their intended failure modes.
- JSONL orphan-backup quarantine can remove the identity-bound source on Windows without weakening replacement detection or data preservation.
- Atomic CLI output publication is asserted with a portable contract, and POSIX mode assertions remain limited to platforms where those bits are meaningful.
- Generated Bun continuation contracts avoid the Windows Git Bash fork bottleneck while retaining every behavioral and adversarial assertion.
- Bootstrap replacement tests accept Windows kernel-enforced rename denial as a successful binding outcome and release every bound handle before cleanup.
- Focused local tests pass, the final GitHub CI and CodeQL runs are green on every configured platform, and the worktree is clean.

## Sub-Tasks

- [x] Repair private-directory fixtures and portable filesystem assertions.
- [x] Make JSONL backup quarantine deletion-safe through an identity-bound Windows handle.
- [x] Remove Git Bash pressure from the Bun continuation contract.
- [x] Align bootstrap replacement tests and handle cleanup with Windows semantics.
- [x] Run focused local verification, archive the TASK, commit, push, and verify final remote gates.

## Notes

- GitHub Actions run `33322438560`, Windows job `99286688289`, is the only failing final gate after TASK 461. macOS, Linux, release trust, LangChain MCP, and CodeQL are green.
- Direct `privatefs` Windows tests pass. The affected CLI, retention, and repository-run tests created `.reconc/run` with `os.MkdirAll`, bypassing the production protected-DACL constructor and sometimes accepting the resulting security error as an unrelated expected failure.
- JSONL orphan quarantine validates both source and preserved copies while holding both files open, then removes the source. Windows denies deletion because the ordinary source handle lacks delete sharing; a share-delete read handle preserves the same identity checks and permits the intended removal.
- The CLI atomic publication test relies on `os.SameFile` changing after replacement, which is not guaranteed by Windows rename semantics. Final bytes and the dedicated atomicfile suite remain the portable publication proof.
- The continuation contract's shell wrapper launches Git Bash for every hook event. Under parallel OpenCode/Kilo drivers this eventually exhausts Windows fork reliability and leaves the Kilo driver alive until the six-minute safety bound.
- The repaired fixtures now call `repositorycontrol.EnsureRunDirectory`, so Windows executes the intended malformed, oversized, reset, follower, and retention paths behind the same protected DACL used by production. The focused CLI, repository-run, retention, and JSONL packages pass locally.
- JSONL backup sources now use a Windows read handle with explicit read, write, and delete sharing. The existing identity, link-count, metadata, content, and post-open path checks remain unchanged, while quarantine and rollback can unlink the verified name before closing the bound handle. Focused orphan-recovery tests pass and the Windows package test binary cross-compiles.
- The high-volume continuation contract now uses a Bun-native hook fixture for logging, bounded outputs, failures, and timeout behavior. Its test-local generated command invokes that fixture directly on Windows, while separate generator tests retain coverage of the production `sh` route. It preserves every behavioral assertion while eliminating the nested Git Bash and Perl process tree; the complete focused continuation contract passes locally.
- Bootstrap replacement tests treat Windows sharing denial as kernel-enforced binding, preserve the owned image, and return mutation errors through the production cleanup path instead of exiting the test goroutine while bound handles are live. Rollback-capable records intentionally retain their bindings after a rejected publication, as required by the existing recovery contract. Focused rollback and replacement tests pass locally, and the Windows bootstrap test binary cross-compiles.
- Final local verification passed: changed-package tests, the complete bootstrap package, the bounded Bun continuation contract, `make test-fast`, `make vet`, `make lint`, Windows amd64 cross-compilation of every changed package, and `git diff --check`. Race, release-trust, and native platform execution remain centralized in the final GitHub run.

## Deviations
