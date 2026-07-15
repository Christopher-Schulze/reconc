# TASK 017: Remove standalone repository self-hosting

## Why

The standalone Reconc product repository must provide bootstrap generators and verification without installing its own generated policy, agent-hook adapters, wrapper, runtime state, or Git hook into itself.

## Acceptance

- The eleven committed self-host artifacts are removed while canonical generators, templates, fixtures, and `bin/hook` remain intact.
- The local Reconc-managed pre-commit hook and ignored `.reconc/` runtime residue are removed.
- Current repository documentation and agent instructions no longer claim or initiate self-hosting.
- Root, harness, race, vet, static analysis, release-trust, and clean-repository bootstrap verification pass.
- The completed change is archived and committed as one TASK.

## Sub-Tasks

- [x] Remove local self-host state and active hook.
- [x] Correct current repository documentation and agent commands.
- [x] Verify the product and clean-repository rollout path.
- [x] Archive and commit the TASK.

## Notes

- `make self-host` remains unchanged: despite its historical name, it builds Reconc and exercises bootstrap against isolated temporary repositories rather than installing Reconc into this source repository.
- Completed TASK 010 remains historical truth and is not rewritten.
- Verification passed for root and template-harness tests, both race suites, both vet runs, pinned Staticcheck v0.7.0, binary build, clean-repository bootstrap across all supported hooks, release trust, and whitespace checks.
- A clean staged-index checkout also passed the product tests and bootstrap golden path without relying on the removed runtime state.

## Deviations

None.
