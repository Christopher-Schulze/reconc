# TASK 541: Redact escaped quoted proof paths

## Why

F2: Escaped quote delimiters leave private path suffixes in generated, verified proof bundles.

## Acceptance

- Encoded quoted external paths leave no private suffix in JSON or Markdown exports; escaping, idempotence, relative paths, and web URLs remain correct.
- Required validation passes without relaxing existing contracts.

## Sub-Tasks

- [ ] Read quote parsing and proof generation; add escaped-delimiter and real export regressions.
- [ ] Track encoded quote boundaries without unescaping unrelated content or exposing embedded path suffixes.
- [ ] Run focused regressions and repository gates; flush documentation, archive, commit, and push origin/main.

## Notes

- Christopher explicitly authorized implementation, one commit per TASK, and push to origin/main.
- Initial source: fb4967fbc9c9c1e1e186ddb7f88d9dff0ffb1f67.
- Use isolated test repositories; preserve all existing private historical details.

## Deviations

None.
