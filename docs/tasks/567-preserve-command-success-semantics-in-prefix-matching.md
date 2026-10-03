# TASK 567: Preserve command success semantics in prefix matching

## Why

Finding F2: the success matcher accepts any normalized string suffix after the
expected prefix. `go test ./... || true` can satisfy `require_command_success`
despite failing tests. Prefix matching is an argument-extension opt-in, not
authorization to substitute the exit status of unrelated shell operations.

## Acceptance

- Prefix success accepts supported additional arguments and output redirects.
- Failure-hiding OR lists, pipelines, sequential suffixes, and background commands
  cannot satisfy a required success merely by sharing its string prefix.
- Quoted or escaped operator-looking arguments remain ordinary arguments.
- Existing explicit command prefixes, exact matching, deny matching, and bounded
  analysis retain their tested contracts; unsupported success extensions fail closed.
- Real evaluator regressions, shell analysis tests, the root suite, vet, lint, and
  development build pass. User docs describe the argument-extension boundary.
- The completed TASK is archived, committed, and pushed to origin/main.

## Sub-Tasks

- [ ] Reproduce failure laundering through the real prefix success evaluator.
- [ ] Bound success extension with the existing shell AST parser and preserve safe forms.
- [ ] Add positive, quoting, compound, and exact/deny compatibility coverage; flush docs.
- [ ] Run validation, review all changes, archive, commit, and push.

## Technical Plan

Keep `commandMatchesExpected` presence/deny semantics intact where appropriate.
Restrict the prefix success path in `matchingCommandResultsSinceWithEvidence`:
additional shell syntax must structurally extend the expected invocation's
arguments while preserving its observed exit-status meaning. Use the existing
mvdan.cc/sh/v3 parser in internal/shellcommand and its analysis bounds. Do not
implement a shell interpreter or infer success from the flattened invocation list,
which deliberately discards shell control-flow context.

Retain exact equality and existing redirect handling. Validate normalized root
`cd` prefixes and RTK normalization without accepting an additional `||`, pipe,
`;`, `&`, negation, or unproven wrapper as success. Use table-driven evaluator
fixtures and parser tests with quoted operators and dynamic words. No new dependency,
runtime process execution, policy kind, or global parser cache is required.

## Notes

Source owners: internal/runtime/evaluator_match.go,
internal/runtime/evaluator_normalize.go, internal/shellcommand/shellcommand.go.
The existing TestCommandMatchPrefixSatisfiesRequireCommandSuccess covers only
ordinary extra arguments and needs adversarial counterparts.

## Deviations

None.
