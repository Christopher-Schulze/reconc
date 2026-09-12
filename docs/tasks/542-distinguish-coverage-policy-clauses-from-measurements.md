# TASK 542: Distinguish coverage policy clauses from measurements

## Why

F3: Word-proximity classification still rejects factual coverage reports and explicitly negated requirements.

## Acceptance

- Factual reports and negated requirements pass; real normative thresholds, assignments, and comparisons remain rejected, including mixed text.
- Required validation passes without relaxing existing contracts.

## Sub-Tasks

- [ ] Read release-trust classification and callers; add failing measurement and negation controls.
- [ ] Classify actual requirement clauses and configuration rather than unrelated nearby words; retain read-error propagation.
- [ ] Run classifier controls and repository gates; flush documentation, archive, commit, and push origin/main.

## Notes

- Christopher explicitly authorized implementation, one commit per TASK, and push to origin/main.
- Initial source: fb4967fbc9c9c1e1e186ddb7f88d9dff0ffb1f67.
- Use isolated test repositories; preserve all existing private historical details.

## Deviations

None.
