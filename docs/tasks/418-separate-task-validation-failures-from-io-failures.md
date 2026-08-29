# TASK 418: Separate TASK validation failures from I/O failures

## Why

TASK commands route both lifecycle validation failures and operational I/O failures through exit code 2. In JSON mode only validation failures receive a body, so machine consumers can see exit 2 with no envelope and cannot distinguish an unreadable repository from a not-ready board.

## Acceptance

- `tasklifecycle.ValidationError` maps to the documented validation/not-ready exit code 2.
- Filesystem, decoding, locking, and other operational failures map to exit code 1.
- `--json` always emits one bounded structured envelope for either class.
- Table tests cover wrapped validation errors, permission/read errors, malformed state, output write failure, and every TASK subcommand using the helper.

## Sub-Tasks

- [ ] Inventory TASK CLI error callers and current JSON schemas.
- [ ] Classify errors with `errors.As` and define the operational envelope.
- [ ] Add exit-code and output-contract regressions.
- [ ] Run focused TASK CLI tests.

## Notes

- Verified from finding 100.
- `writeTaskFailure` currently always constructs `CLIError{ExitCode: 2}` and emits `{"valid":false}` only for validation errors.

## Deviations
