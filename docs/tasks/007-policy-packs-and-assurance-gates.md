# TASK 007: Policy packs and assurance gates

## Why

Golem contains valuable control patterns mixed with Omnimus assumptions. The
standalone product needs configurable, stack-aware assurance packs and gate
contracts, not copied project rules or a permanently growing monolithic
workflow audit.

## Acceptance

- Policy packs compose through explicit capabilities, inputs, evidence, and deterministic conflict rules.
- Generic gates cover repository layout, generated-reference integrity, language boundaries, dependency pins, network/process boundaries, substantive proof, and live build/test truth only when configured and applicable.
- Stack detection proposes packs but never silently locks a repository into guessed policy.
- Every imported Golem pattern is stripped of Omnimus paths, names, baselines, and historical evidence.
- Gates are diff-aware where safe, fail closed where authority matters, and expose exact remediation.
- Positive, negative-control, mutation, bypass, and scale tests prove effectiveness rather than theater.

## Sub-Tasks

- [~] Extract reusable gate contracts from Golem without copying product policy.
- [ ] Define composable pack capabilities and configuration schemas.
- [ ] Implement the smallest high-value generic gate set.
- [ ] Add adversarial effectiveness and bypass proofs.
- [ ] Document pack selection, extension, and bootstrap integration.

## Notes

Approved areas: 19 Policy pack architecture; 20 Generic assurance gates.

## Deviations

None.
