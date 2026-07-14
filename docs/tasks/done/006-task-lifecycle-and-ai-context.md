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

- [x] Define the typed lifecycle schema and repository-adoption boundary.
- [x] Implement read, validation, and atomic mutation commands.
- [x] Move reusable TASK gates out of project-specific audit code.
- [x] Build compact delta briefings and repeated-feedback suppression.
- [x] Prove lifecycle races, archive scale, token bounds, and docs behavior.

## Notes

Approved areas: 13 Task lifecycle into core CLI; 21 Token-efficient AI control.

`sections-v1` is the bounded canonical profile for new repositories;
`logbook-v1` adopts Golem-style Current/detail state without migration. Ordinary
reads never reopen archived detail files. Mutations use a cross-platform lock,
an integrity-checked recovery journal, atomic file publication, and verified
rename preconditions.

The product command adopted Golem's live `TASK-2359` control plane read-only
without a migration. A 5,000-row logbook benchmark exposed and removed an
O(n²) duplicate scan: latency fell from 109-121 ms to 3.66 ms and allocations
from about 112 MB to 4.15 MB, including current path-safety validation. Twenty
full CLI status processes against the real Golem board completed in 0.41
seconds total. The sectioned profile stayed archive-size independent at about
106 microseconds with 200 unlinked archived details.

Final proof passed the full test suite, race suite, Vet, Staticcheck, the nested
harness-template suite, standalone self-validation, and read-only validation
against Golem. Recovery tests cover combined content mutation plus rename,
exact mode restoration, external-edit conflicts, symlink rejection, and
bounded evidence context.

## Deviations

None.
