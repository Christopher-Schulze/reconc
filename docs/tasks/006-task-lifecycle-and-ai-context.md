# TASK 006: Task lifecycle and AI context

## Why

Repositories currently depend on project-specific audit code for TASK truth and
feed agents long, repetitive context. The universal product needs a small
typed TASK lifecycle plus compact, delta-oriented briefings that preserve hard
control without wasting tokens.

## Acceptance

- Reconc parses and validates the canonical TASK overview/detail lifecycle without project-specific Go audits.
- Claim, promote, block, resume, split, archive, and completion checks are atomic, fail closed, and non-destructive.
- Existing repository conventions are adopted through configuration instead of forcibly migrated.
- Session briefings expose only current TASK, current sub-task, blockers, relevant policy deltas, required evidence, and exact remediation.
- Repeated hook feedback collapses to stable identifiers and saved report paths.
- Token and latency benchmarks prove bounded output as task archives grow.

## Sub-Tasks

- [~] Define the typed lifecycle schema and repository-adoption boundary.
- [ ] Implement read, validation, and atomic mutation commands.
- [ ] Move reusable TASK gates out of project-specific audit code.
- [ ] Build compact delta briefings and repeated-feedback suppression.
- [ ] Prove lifecycle races, archive scale, token bounds, and docs behavior.

## Notes

Approved areas: 13 Task lifecycle into core CLI; 21 Token-efficient AI control.

## Deviations

None.
