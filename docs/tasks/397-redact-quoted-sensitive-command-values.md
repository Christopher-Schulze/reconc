# TASK 397: Redact quoted sensitive command values

## Why

Impact-corpus sanitization tokenizes with `strings.Fields` and replaces only one token after a sensitive flag. Multi-word quoted values therefore leak every token after the first into an export artifact.

## Acceptance

- Sensitive flag values are redacted through a matching quote boundary.
- Unterminated quoted sensitive values are redacted through end-of-input.
- Escaped quotes, single quotes, double quotes, bearer forms, assignments, URLs, and ordinary arguments retain deterministic behavior.
- Adversarial regressions prove no suffix of a sensitive quoted value survives export.

## Sub-Tasks

- [ ] Define a bounded shell-text redaction scanner without executing or fully parsing commands.
- [ ] Replace token-only sensitive-flag handling.
- [ ] Add adversarial privacy fixtures and corpus round-trip checks.
- [ ] Run focused impact-lab tests.

## Notes

- Verified from finding 55.
- Example confirmed by current code: `--password "secret extra words"` redacts only the first post-flag token.

## Deviations
