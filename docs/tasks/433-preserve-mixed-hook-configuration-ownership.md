# TASK 433: Preserve mixed hook configuration ownership

## Why

Hook merging classifies some nested groups from only their first command, replaces non-array event values despite a preservation comment, overwrites the full Antigravity `reconc` namespace, and misses exec-form Reconc commands in its ownership prefilter. Existing empty files are also reported as newly created.

## Acceptance

- Every nested hook command is classified independently and all foreign siblings survive in stable order.
- Non-array event values and foreign Antigravity namespace entries are preserved or require explicit force with a complete dropped-edit report.
- Shell- and exec-form Reconc commands share one parsed ownership contract.
- Existing empty objects/files are reported truthfully as updates without treating semantic emptiness as foreign content loss.
- Adversarial tests cover mixed groups, malformed shapes, NUL-separated signatures, duplicate managed commands, and namespace collisions.

## Sub-Tasks

- [ ] Model ownership at individual command and namespace-entry granularity.
- [ ] Replace whole-group/whole-namespace deletion with preservation-first merging.
- [ ] Align parsed signature prefilters and install action reporting.
- [ ] Run focused hook merge and installer tests.

## Notes

- Verified from findings 118, 119, 120, 121, and 123.

## Deviations
