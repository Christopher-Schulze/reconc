# TASK 261: Improve policy authoring UX

## Why

Policy onboarding currently exposes powerful but separate commands: `adopt`
suggests configuration, compilation validates the full source graph, and
`explain` evaluates evidence against an existing lock. Authors need one guided
workflow that validates a candidate, explains its effective rules and impact,
and applies it only after explicit confirmation, while preserving Reconc's
non-interactive and fail-closed automation contracts.

## Acceptance

- A canonical policy-authoring command accepts a candidate policy or detected
  repository recommendation and performs validate, explain, and adopt phases
  without mutating during validation or explanation.
- Validation uses the shipped policy-config JSON Schema plus the real parser,
  compiler, source-conflict, template, preset, and semantic checks. Schema-only
  success is never presented as compile success.
- Explanation shows effective packs, normalized rules, source provenance,
  conflicts, affected rule kinds, and an Impact Lab delta when replay evidence
  is supplied, with deterministic privacy-bounded JSON for automation.
- Adoption requires an explicit apply flag or an interactive terminal
  confirmation, writes only the selected repository-owned policy target via an
  atomic identity-checked transaction, recompiles, verifies, and rolls back its
  own publication on failure.
- Non-terminal input never prompts; JSON mode is non-interactive; cancellation
  and declined adoption leave repository bytes and lock identity unchanged.
- Command metadata, help, completion, manpage, generated references, docs, and
  tests cover the full workflow, malformed input, conflicts, cancellation,
  successful apply, rollback, and path-safety boundaries.

## Sub-Tasks

- [x] Map existing adopt, compile, explain, impact, and schema contracts
- [x] Define the policy-authoring request, report, and mutation boundary
- [x] Implement schema-backed candidate validation and effective explanation
- [x] Implement explicit and terminal-confirmed transactional adoption
- [x] Integrate command metadata, help, completion, manpage, and references
- [x] Add unit, integration, cancellation, rollback, and privacy regressions
- [x] Update product and contributor documentation
- [x] Run focused, race, release-trust, and repository-wide verification
- [x] Archive the completed TASK and commit the verified change

## Notes

- Existing `reconc adopt` behavior remains available for compatibility unless
  the new workflow fully subsumes it and every caller and reference is migrated
  atomically.
- The workflow must compose existing parser/compiler/impact capabilities rather
  than introduce a second policy semantics implementation.
- The canonical command is `reconc policy author`. A policy-file target cannot
  own `extends`; detected pack suggestions therefore remain review-only while
  detected individual rules form the candidate fragment.
- Preview uses a virtual real target path and exact discovery metadata, so its
  lock bytes are identical to the subsequent production compile.
- JSON and non-terminal execution never prompt. Only explicit `--apply` or an
  affirmative text-terminal response crosses the mutation boundary.
- Final verification passed the complete root/template race gate, release
  trust with real temporary artifact tampering, publication and reference
  audits, root/template coverage, vet, pinned Staticcheck, tidy, pinned
  Govulncheck, self-hosting, and the locked Python 3.13.14 LangChain proof.
- Final measured coverage is 82.6679% for the root module and 84.0628% for the
  harness template. The shared YAML decoder is covered through parser and
  schema consumers at 96.2%; its bounds walker is covered at 75.9%.

## Deviations

None.
