# TASK 418: Separate TASK validation failures from I/O failures

## Why

TASK commands route both lifecycle validation failures and operational I/O failures through exit code 2. In JSON mode only validation failures receive a body, so machine consumers can see exit 2 with no envelope and cannot distinguish an unreadable repository from a not-ready board.

## Acceptance

- `tasklifecycle.ValidationError` maps to the documented validation/not-ready exit code 2.
- Filesystem, decoding, locking, and other operational failures map to exit code 1.
- `--json` always emits one bounded structured envelope for either class.
- Table tests cover wrapped validation errors, permission/read errors, malformed state, output write failure, and every TASK subcommand using the helper.

## Sub-Tasks

- [x] Inventory TASK CLI error callers and current JSON schemas.
- [x] Classify errors with `errors.As` and define the operational envelope.
- [x] Add exit-code and output-contract regressions.
- [x] Run focused TASK CLI tests.

## Notes

- Verified from finding 100.
- `writeTaskFailure` currently always constructs `CLIError{ExitCode: 2}` and emits `{"valid":false}` only for validation errors.
- Confirmed on current source: every lifecycle-backed TASK subcommand reaches `writeTaskFailure` directly or through `writeTaskMutation`, but the helper ignores JSON write failures, emits no operational envelope, and assigns exit 2 without inspecting the error chain.
- Existing validation JSON consumers depend on `valid: false` and structured `issues`; the additive `failure_class` discriminator preserves those fields while giving operational failures a separate bounded `error` field.
- Failure JSON is capped at 64 KiB. Oversized validation detail retains one field-bounded issue plus `omitted_issues`; operational messages are capped at 4 KiB. Encoding, write, and short-write failures return exit 1 instead of being hidden behind the original classification.
- Regression coverage includes wrapped validation errors, permission reads, malformed transaction state, a failing output writer, oversized issue data, one-envelope framing, actual missing-repository and recovery paths, and all eleven TASK subcommands routed through the helper.
- Verification passed: focused TASK failure tests, the complete `internal/cli` package, and `make test-fast`.

## Deviations

- TASK activation metadata was applied immediately after the first focused implementation/test pass instead of before the edit; no other TASK was active, modified, or committed during that interval.
