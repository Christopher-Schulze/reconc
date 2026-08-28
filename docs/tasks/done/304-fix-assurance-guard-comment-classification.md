# TASK 304: Fix assurance guard comment classification

## Why

The assurance guard treats every trimmed line beginning with `*` as comment-only. Valid code such as `*ptr = dangerousCall()` can therefore bypass site-pattern enforcement.

## Acceptance

- Comment classification is syntax-aware enough for every supported scanned language and never treats ordinary dereference or multiplication code as a comment.
- Existing block-comment interiors, line comments, HTML comments, configured markers, and exemptions retain their behavior.
- Regression tests prove guard sites at the first non-whitespace character are still detected.
- Assurance and publication gates pass.

## Sub-Tasks

- [x] Define language-specific comment-only recognition
- [x] Replace the ambiguous bare-star rule
- [x] Add adversarial comment and code-line tables
- [x] Run assurance and full source-hygiene tests

## Notes

- Evidence: `internal/assurance/gates.go:180-236`.
- Contract: guard scans classify line comments by language and track slash, HTML, HEEx, and PowerShell block-comment state across lines. A leading `*` is comment-only only while an actual block comment is open.
- Verification: adversarial assurance tests, `make test`, `make vet`, pinned Staticcheck v0.8.1, and `make self-host` pass.

## Deviations

None.
