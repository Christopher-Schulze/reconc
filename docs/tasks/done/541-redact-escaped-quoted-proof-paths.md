# TASK 541: Redact escaped quoted proof paths

## Why

F2: Escaped quote delimiters leave private path suffixes in generated, verified proof bundles.

## Acceptance

- Encoded quoted external paths leave no private suffix in JSON or Markdown exports; escaping, idempotence, relative paths, and web URLs remain correct.
- Required validation passes without relaxing existing contracts.

## Sub-Tasks

- [x] Read quote parsing and proof generation; add escaped-delimiter and real export regressions.
- [x] Track encoded quote boundaries without unescaping unrelated content or exposing embedded path suffixes.
- [x] Run focused regressions and repository gates; flush documentation, archive, commit, and push origin/main.

## Notes

- Christopher explicitly authorized implementation, one commit per TASK, and push to origin/main.
- Initial source: fb4967fbc9c9c1e1e186ddb7f88d9dff0ffb1f67.
- Use isolated test repositories; preserve all existing private historical details.
- Original-code failures are retained in `.build/review-repairs/task541-red.log`. Tests include four encoding depths, embedded quotes, trailing path backslashes, multiple paths, an encoded object, unterminated delimiters, and real JSON/Markdown generation from persisted policy evidence.
- Parse quote tokens before UNC spans; retain delimiter encoding width and distinguish encoded closing delimiters from embedded quotes. No decoding of unrelated text or change to structured path admission is required.
- Validation: complete proofbundle race suite, `make test-fast` (root and portable template), `make vet`, `make lint`, `make build`, and `git diff --check` passed. Logs are retained under `.build/review-repairs/task541-*.log`; TASK 542 runs the final integrated uncached race and release-trust gate.

## Deviations

None.
