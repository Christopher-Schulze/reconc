# TASK 569: Enforce module-scoped causal freshness in native assurance

## Why

Finding F4: runtime and completion capture flatten successful command evidence
into strings before native assurance receives it. The receiving contract has no
write epochs, so live-verification and other command-backed native gates can
reuse successes preceding a relevant write.

## Acceptance

- A successful command older than a relevant write cannot satisfy a native
  command-backed assurance gate; an equal/newer epoch can.
- Unrelated module writes do not invalidate another module's applicable evidence.
- Root, nested module, workspace-manager, and directory-scoped command evidence
  retain correct ownership. Explicit index-bound proof epochs remain accepted.
- Runtime evaluation and completion input identity use the same causal data;
  legacy zero-epoch inputs remain compatible only when no newer required epoch exists.
- Live/generated/package/substantive command evidence paths are reviewed and tested.
- Targeted assurance/runtime/completion tests, the root suite, vet, lint, and
  development build pass; docs state the causal and module-scoped contract.
- The completed TASK is archived, committed, and pushed to origin/main.

## Sub-Tasks

- [ ] Add stale/fresh and independent-module regressions against real native gates.
- [ ] Carry command epochs and write epochs through existing assurance inputs.
- [ ] Filter command-backed gates at the effective module scope and align completion identity.
- [ ] Verify all importing callers and compatible trusted proofs; flush docs and run gates.
- [ ] Review all changes, archive, commit, and push.

## Technical Plan

Extend the concrete internal assurance input shapes with causal evidence; retain
the existing directory-aware CommandEvidence and reuse scope.effectivePaths for
repository-relative epoch selection. Compute the minimum relevant epoch per
module, or from the gate's relevant root paths when no module applies, rather
than dropping commands at a repository-global epoch before scoping.

Update evalRequireAssurance and CaptureCompletionState's assurance identity call
to pass the same successful records and write epochs. Keep raw command reporting
stable and normalized command equivalence intact. Review scopeCommandEvidence,
scopePackageScriptEvidence, substantive proof command matching, and the input
identity serializer so no alternate command-backed gate bypasses freshness.
Preserve ExplicitEvidenceEpoch as the existing trusted-proof boundary. No lockfile
schema change, clock threshold, new policy option, dependency, or global cache.

## Notes

Source owners: internal/runtime/evaluator_rules.go,
internal/runtime/agentsession/completion_state.go,
internal/assurance/assurance.go, module_scope.go, gates.go, facts.go.

## Deviations

None.
