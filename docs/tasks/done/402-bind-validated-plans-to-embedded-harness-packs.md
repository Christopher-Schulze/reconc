# TASK 402: Bind validated plans to embedded harness packs

## Why

`ValidatePlan` checks only the shape of an advanced harness-pack selection. Exact name, version, and digest binding to the embedded product pack occurs later during apply, so other consumers can accept and report a fabricated but syntactically valid pack identity.

## Acceptance

- An advanced plan validates only when its selected harness pack exactly matches the embedded pack for `ProductVersion`.
- Load, write, replace, verify, status, and apply share the same identity rule.
- Unknown or unavailable product versions fail with one explicit compatibility error.
- Adversarial tests mutate pack name, version, digest, product version, and artifact digests across every plan consumer.

## Sub-Tasks

- [x] Map every `ValidatePlan` caller and embedded-pack lookup signature.
- [x] Move exact pack binding into the canonical plan validation boundary.
- [x] Remove redundant later checks only if coverage and error semantics remain exact.
- [x] Run focused bootstrap and harness tests.

## Notes

- Verified from finding 68.
- Before the fix, `validateHarnessPackSelections` checked only non-empty identity shape; exact comparison was reached only while building artifacts.
- `ValidatePlan` now resolves the embedded pack for the plan product version, compares the exact selection, and binds every pack action path, component, mode, and authenticated file digest.
- Load, write, replace, verify, apply, remove, managed-candidate acceptance, and repository-receipt construction return the same canonical validation error for forged plans.
- The later artifact renderer reuses the already exact pack lookup instead of validating and loading it twice.
- A historical test that encoded an intentionally incompatible `0.8.8` advanced plan contradicted the new contract. It now persists adversarial bytes directly and proves both plan loading and repository sync reject them.
- `docs/documentation.md` already states that plan and receipt pack identities are embedded-pack-bound and that unknown legacy digests never receive invented compatibility bounds.
- Verification: adversarial bootstrap consumer tests, advanced initialization and harness tests, `make test-fast`, and `git diff --check` passed on macOS.

## Deviations
