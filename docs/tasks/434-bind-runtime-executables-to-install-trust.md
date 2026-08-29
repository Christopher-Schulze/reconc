# TASK 434: Bind runtime executables to install trust

## Why

The repository wrapper rejects symlinks only for its receipt-selected binary, not dev/dist fallbacks. Kimi hooks execute the first `reconc` found on the future PATH even though installation verified a potentially different binary.

## Acceptance

- Every wrapper executable candidate uses the same regular-file, non-symlink, executable, and identity checks.
- Kimi runtime execution is bound to a verified installation receipt, repository wrapper, or exact executable identity rather than ambient PATH drift.
- Relocation/update behavior is explicit and produces actionable failure instead of silently trusting another binary.
- Adversarial tests cover symlink swaps, PATH precedence changes, replacement after install, missing receipts, and valid upgrades.

## Sub-Tasks

- [ ] Define one executable trust contract across receipt, dev, dist, and Kimi paths.
- [ ] Bind generated Kimi commands without embedding unverifiable mutable authority.
- [ ] Add deterministic identity-swap and PATH-drift regressions.
- [ ] Run focused wrapper and Kimi tests.

## Notes

- Verified from findings 122 and 130.

## Deviations
