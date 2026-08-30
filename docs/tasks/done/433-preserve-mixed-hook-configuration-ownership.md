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

- [x] Model ownership at individual command and namespace-entry granularity.
- [x] Replace whole-group/whole-namespace deletion with preservation-first merging.
- [x] Align parsed signature prefilters and install action reporting.
- [x] Run focused hook merge and installer tests.

## Notes

- Verified from findings 118, 119, 120, 121, and 123.
- Current-code verification confirmed all five findings: flat hook merging classified a mixed nested group as one entry, non-array event values were overwritten after only a warning, Antigravity replaced the complete existing `reconc` namespace, the ownership text prefilter rejected valid NUL-separated exec signatures before parsing them, and semantic empty targets retained the initial `created` action despite an existing file identity.
- Merge conflicts now fail before target publication unless `--force` is explicit. Forced replacements enumerate the exact container/event and observed JSON type; command-level replacements enumerate every modified Reconc command while preserving foreign order.
- Focused merge, ownership, Claude/Antigravity installer, malformed-shape, namespace-collision, and empty-target tests passed in 0.754 seconds. Generated-reference validation and `git diff --check` passed. Full, race, vet, lint, release-trust, and platform gates remain deferred to the requested queue-end pass.

## Deviations
