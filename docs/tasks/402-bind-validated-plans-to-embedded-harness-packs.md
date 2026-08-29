# TASK 402: Bind validated plans to embedded harness packs

## Why

`ValidatePlan` checks only the shape of an advanced harness-pack selection. Exact name, version, and digest binding to the embedded product pack occurs later during apply, so other consumers can accept and report a fabricated but syntactically valid pack identity.

## Acceptance

- An advanced plan validates only when its selected harness pack exactly matches the embedded pack for `ProductVersion`.
- Load, write, replace, verify, status, and apply share the same identity rule.
- Unknown or unavailable product versions fail with one explicit compatibility error.
- Adversarial tests mutate pack name, version, digest, product version, and artifact digests across every plan consumer.

## Sub-Tasks

- [ ] Map every `ValidatePlan` caller and embedded-pack lookup signature.
- [ ] Move exact pack binding into the canonical plan validation boundary.
- [ ] Remove redundant later checks only if coverage and error semantics remain exact.
- [ ] Run focused bootstrap and harness tests.

## Notes

- Verified from finding 68.
- `validateHarnessPackSelections` currently checks non-empty identity shape; `validateHarnessPacks` owns the exact comparison but is reached only while building artifacts.

## Deviations
