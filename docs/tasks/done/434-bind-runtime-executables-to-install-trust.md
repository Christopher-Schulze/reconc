# TASK 434: Bind runtime executables to install trust

## Why

The repository wrapper rejects symlinks only for its receipt-selected binary, not dev/dist fallbacks. Kimi hooks execute the first `reconc` found on the future PATH even though installation verified a potentially different binary.

## Acceptance

- Every wrapper executable candidate uses the same regular-file, non-symlink, executable, and identity checks.
- Kimi runtime execution is bound to a verified installation receipt, repository wrapper, or exact executable identity rather than ambient PATH drift.
- Relocation/update behavior is explicit and produces actionable failure instead of silently trusting another binary.
- Adversarial tests cover symlink swaps, PATH precedence changes, replacement after install, missing receipts, and valid upgrades.

## Sub-Tasks

- [x] Define one executable trust contract across receipt, dev, dist, and Kimi paths.
- [x] Bind generated Kimi commands without embedding unverifiable mutable authority.
- [x] Add deterministic identity-swap and PATH-drift regressions.
- [x] Run focused wrapper and Kimi tests.

## Notes

- Verified from findings 122 and 130.
- The wrapper now admits every dev, direct, stable, versioned, and PATH fallback only through one regular, non-symlink, executable predicate.
- Kimi commands carry `receipt-v1`; install/status bind PATH to the installation receipt, and initialized-repository runtime verifies its own receipt path and SHA-256 before policy evaluation.
- Regressions cover all wrapper candidate symlinks, PATH precedence drift, post-install replacement, missing receipts, legacy generated commands, and a receipt-backed upgrade repair.
- Focused wrapper, Kimi, user-CLI identity, and generated-reference tests passed; `git diff --check` passed. Queue-end gates remain deferred by operator instruction.

## Deviations
