# TASK 169: Stabilize Windows hard-link security proof

## Why

Native Windows candidate run `31620600092` proved the production Action Ledger
packages green but exposed a test-double defect in `internal/jsonl`: `os.Lstat`
defers Windows file-ID loading until `os.SameFile`, so a recorded candidate path
could no longer resolve its identity after the production code removed that
hard-link name.

## Acceptance

- JSONL security test doubles capture file identity from an open handle before
  a temporary hard-link name can disappear.
- The existing durable-file security regression remains strict and passes on
  native Windows without adding a path-name shortcut.
- Focused tests, Windows cross-compilation, and complete candidate CI pass with
  source version exactly `0.9.6`.

## Sub-Tasks

- [x] Capture eager cross-platform file identity in security test doubles
- [x] Run focused local proof and Windows cross-compilation
- [x] Reconcile TASK truth and prepare the exact candidate commit

## Notes

Go's Windows `os.Lstat` records a path and loads the volume/file ID lazily.
`(*os.File).Stat` obtains the identity from the live handle immediately. The
production publication and DACL validation passed; only the mock's delayed
identity lookup failed after its candidate pathname was removed.

The complete JSONL package passed 100 consecutive times, the native Windows
test binary cross-compiled, and the publication and harness-pack audits passed.

## Deviations

None.
