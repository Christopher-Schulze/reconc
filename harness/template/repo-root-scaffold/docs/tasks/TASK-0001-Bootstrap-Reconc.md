# TASK-0001-Bootstrap-Reconc

## Why

The repository needs a durable local workflow baseline before product work starts: Reconc must be copied, renamed, configured for the selected stack, and verified against the repo's actual structure.

## Status

State: Active

## Scheduling

- Priority: P0
- Depends On: none
- Parallel Group: none
- Expected Touch Surfaces: AGENTS.md, start.md, docs/**, tools/reconc/**, .reconc.yml, .codex/**, .cursor/**, .agents/**, .claude/**, .opencode/**
- Order Rationale: Bootstrap must run before any product TASK so every later edit is governed by the same task, hook, audit, and documentation rules.
- Scope Type: Audit Repair
- Spec Lines: docs/spec.md:L1-L3
- Spec Bindings: docs/spec.md:L1-L3@sha256:ea76978a6f90e675e28c9a3fbaf06b30ad8cec0268d6a6898af67bcf31f8f321@project+workflow
- Research Refs: none
- Completion Claim: Done means Reconc governance is installed, rebranded, verified, and ready to govern the next project TASK.

## Technical Plan

Read `tools/reconc/harness/template/BOOTSTRAP.md`, confirm the selected stack and layout, merge the workflow excerpts non-destructively, rebrand `project` placeholders, install repo-local hooks, run Reconc status/session/audits, and record the exact verification evidence here. Same-TASK tests are required if bootstrap changes any code or durable tooling.

- Read and follow `tools/reconc/harness/project/BOOTSTRAP.md`.
- Confirm target repo layout: flat-root default, `codebase/` only when already present or explicitly selected.
- Confirm selected stack from real repo files and user confirmation when detection is uncertain.
- Merge `AGENTS.md` excerpt and `.gitignore.excerpt` without overwriting existing files.
- Rebrand `project` / `Project` / `PROJECT` placeholders across copied workflow files.
- Install `.reconc.yml`, local hooks and stack config.
- Run Reconc, harness and selected-stack checks before marking this TASK done.

## Acceptance

- Reconc status reports a fresh policy lock and configured rules.
- `tools/reconc/harness/project/audits/run-workflow-audit all` passes or every stack-intentional skip is documented.
- Hook configs resolve repo-local Reconc binaries before PATH fallback.
- `AGENTS.md`, `start.md`, `docs/tasks.md`, `docs/documentation.md`, `.reconc.yml`, and stack config are present and rebranded.
- Test/build evidence for the selected stack is recorded in Notes.

## Sub-Tasks

- [~] Read BOOTSTRAP.md and verify selected stack/layout with the user when auto-detection is uncertain.
- [ ] Rebrand and place Reconc harness/template files.
- [ ] Merge AGENTS.md and .gitignore excerpts without overwriting existing repo content.
- [ ] Install hook configs and .reconc.yml.
- [ ] Run status, audit, build and test checks.
- [ ] Complete Final Reality Check and archive this TASK when all acceptance checks pass.

## Notes

- Bootstrap-created starter TASK. Replace or extend these notes with real verification evidence during rollout.

## Deviations

- None.
