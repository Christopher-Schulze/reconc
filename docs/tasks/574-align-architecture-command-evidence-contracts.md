# TASK 574: Align architecture command evidence contracts

## Why

The final documentation cross-check found that docs/architecture.md still says
Reconc-owned command outcomes use the current epoch at completion. TASK 568
binds them before execution. The neighboring prefix-matching section also needs
the success-extension boundary implemented by TASK 567. Keep architecture and
the canonical user documentation consistent without changing runtime behavior.

## Acceptance

- Architecture describes the captured start epoch and session generation,
  including session switch/reset and no-session behavior.
- Architecture distinguishes post-only host observations from Reconc-owned
  start evidence and preserves the separate staged proof contract.
- Prefix success extensions describe static argument additions without accepting
  control flow, pipelines, background work, or dynamic substitutions as success.
- Wording agrees with the actual code, regression tests, and user documentation;
  generated reference checks pass. No implementation or generated block changes.
- The TASK is archived, committed, and pushed separately before central final
  race/static validation.

## Sub-Tasks

- [ ] Verify affected architecture paragraphs against code and user documentation.
- [ ] Patch only stale contract wording and review semantic consistency.
- [ ] Run reference checks, archive, commit, and push.

## Technical Plan

Read architecture's causal command-success and redirect-tolerance sections;
cross-check internal/cli/exec_cmd.go, agentsession/command.go, and the bounded
shell success-prefix matcher. Retain causal write filtering and exact staged
receipts. Replace only the epoch-owner wording and add the explicit structural
prefix-success boundary already documented in docs/documentation.md. Preserve
the generated matrices, unchanged presence/deny behavior, and independent host
post-event provenance. Use existing reference checks and diff review; no test
or runtime abstraction is needed for this documentation correction.

## Notes

Discovered while propagating TASK 573's architecture package-map entry. The
existing architecture epoch claim is at the causal command-success section;
docs/documentation.md already contains the updated execution binding contract.
This is required propagation of TASKs 567 and 568, not a new feature.

## Deviations

None.
