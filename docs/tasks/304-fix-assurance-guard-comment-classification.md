# TASK 304: Fix assurance guard comment classification

## Why

The assurance guard treats every trimmed line beginning with `*` as comment-only. Valid code such as `*ptr = dangerousCall()` can therefore bypass site-pattern enforcement.

## Acceptance

- Comment classification is syntax-aware enough for every supported scanned language and never treats ordinary dereference or multiplication code as a comment.
- Existing block-comment interiors, line comments, HTML comments, configured markers, and exemptions retain their behavior.
- Regression tests prove guard sites at the first non-whitespace character are still detected.
- Assurance and publication gates pass.

## Sub-Tasks

- [ ] Define language-specific comment-only recognition
- [ ] Replace the ambiguous bare-star rule
- [ ] Add adversarial comment and code-line tables
- [ ] Run assurance and full source-hygiene tests

## Notes

- Evidence: `internal/assurance/gates.go:180-236`.

## Deviations

None.
