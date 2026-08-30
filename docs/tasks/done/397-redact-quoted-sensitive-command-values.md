# TASK 397: Redact quoted sensitive command values

## Why

Impact-corpus sanitization tokenizes with `strings.Fields` and replaces only one token after a sensitive flag. Multi-word quoted values therefore leak every token after the first into an export artifact.

## Acceptance

- Sensitive flag values are redacted through a matching quote boundary.
- Unterminated quoted sensitive values are redacted through end-of-input.
- Escaped quotes, single quotes, double quotes, bearer forms, assignments, URLs, and ordinary arguments retain deterministic behavior.
- Adversarial regressions prove no suffix of a sensitive quoted value survives export.

## Sub-Tasks

- [x] Define a bounded shell-text redaction scanner without executing or fully parsing commands.
- [x] Replace token-only sensitive-flag handling.
- [x] Add adversarial privacy fixtures and corpus round-trip checks.
- [x] Run focused impact-lab tests.

## Notes

- Verified from finding 55.
- Example confirmed by current code: `--password "secret extra words"` redacts only the first post-flag token.
- The pre-fix regression leaked suffix words for double-quoted, single-quoted, unterminated, escaped-quote, assignment, and Bearer-header forms.
- The scanner now groups shell-like words with quote and backslash awareness, caps retained word slices at 4,097, and never executes or expands input.
- Focused privacy and corpus tests, the complete `internal/impactlab` package, and `make test-fast` passed.

## Deviations
