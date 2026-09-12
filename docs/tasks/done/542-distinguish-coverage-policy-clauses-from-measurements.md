# TASK 542: Distinguish coverage policy clauses from measurements

## Why

F3: Word-proximity classification still rejects factual coverage reports and explicitly negated requirements.

## Acceptance

- Factual reports and negated requirements pass; real normative thresholds, assignments, and comparisons remain rejected, including mixed text.
- Required validation passes without relaxing existing contracts.

## Sub-Tasks

- [x] Read release-trust classification and callers; add failing measurement and negation controls.
- [x] Classify actual requirement clauses and configuration rather than unrelated nearby words; retain read-error propagation.
- [x] Run classifier controls and repository gates; flush documentation, archive, commit, and push origin/main.

## Notes

- Christopher explicitly authorized implementation, one commit per TASK, and push to origin/main.
- Initial source: fb4967fbc9c9c1e1e186ddb7f88d9dff0ffb1f67.
- Use isolated test repositories; preserve all existing private historical details.
- Replace word proximity with clause-bounded requirements and concrete configuration syntax. Remove only explicit negated requirement phrases, retaining adjacent positive requirements and prohibitive bounds. Keep configuration/comparison detection independent of prose negation.
- Original-code failure is retained in `.build/review-repairs/task542-red.log`. Controls cover decimal measurements, sentence boundaries, negation, mixed requirements, non-UTF-8 repository cache content, and distinct file-read failures. Byte-oriented scanning avoids host-locale failures without ignoring file contents.
- Validation passed: all classifier controls and the repository-wide scan, classifier ShellCheck, shell syntax, `make test` (publication audit, uncached root/template race suites, release trust with a real 70-second release fixture), `make vet`, `make lint`, `make self-host` (including host build), and `git diff --check`. Evidence is retained under `.build/review-repairs/task542-*.log`. All 25 original private historical task files remain unchanged, untracked, and ignored.

## Deviations

None.
