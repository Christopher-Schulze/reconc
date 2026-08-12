# TASK 170: Serialize private Windows directory initialization

## Why

Native Windows candidate run `31622048216` exposed a real first-use race:
multiple processes could create one private project directory concurrently,
and a process observing the directory before its creator protected the DACL
failed closed instead of converging on the completed private boundary.

## Acceptance

- Project action-directory initialization is serialized with retention and
  concurrent initializers across processes.
- Existing unsafe directories remain rejected without repair.
- The multiprocess Action Ledger regression releases all helpers together and
  proves the initialized private boundary plus serialized ledger state.
- Focused tests and complete candidate CI pass on native Windows with source
  version exactly `0.9.6`.

## Sub-Tasks

- [x] Serialize private project-directory initialization under the retention lock
- [x] Strengthen the multiprocess first-use regression
- [x] Reconcile release and TASK truth
- [x] Run focused and complete release gates

## Notes

The retention lock already defines the cross-process boundary between project
root deletion and creation. Initialization incorrectly acquired it shared, so
other creators could observe a newly created Windows directory between
`MkdirAll` and protected-DACL publication.

The synchronized Action Ledger multiprocess regression passed 100 consecutive
runs and the adjacent action-state multiprocess regression passed 50. Both
affected Windows test packages cross-compiled. Complete race tests, release
trust, vet, static analysis, whole-module coverage, all 50 discovered fuzz
targets, both vulnerability scans, the pinned LangChain proof, self-hosting,
and the real five-target `0.9.6` release build passed locally.

Candidate CI `31624884426` passed every job at commit `2dfcd29`, including the
complete native Windows test, binary smoke, and installer gates. CodeQL run
`31624884481` passed against the same commit.

## Deviations

None.
