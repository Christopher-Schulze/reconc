# TASK 439: Redact CI host paths on boundaries

## Why

CI operational-error redaction performs raw substring replacement for repository and home paths. Neighboring path components can be partially rewritten, producing misleading output and leaving user/host fragments outside a canonical path token.

## Acceptance

- Repository and home replacements match complete path components or validated absolute-path tokens on Unix and Windows.
- Neighbor paths and ordinary text containing the same substring are not corrupted.
- No absolute host path, home fragment, or username-bearing path survives rendered JSON, JUnit, SARIF, or text output.
- Adversarial tests cover prefix collisions, drive letters, separators, punctuation, multiple paths, Unicode, and short home names.

## Sub-Tasks

- [x] Define one boundary-aware host-path tokenizer for CI reports.
- [x] Apply it before public text bounding without exposing raw paths.
- [x] Add renderer-level privacy regressions.
- [x] Run focused CI report tests.

## Notes

- Verified from finding 166 in `internal/cireport/model.go`.
- Confirmed: raw `strings.ReplaceAll` rewrote configured-root prefixes inside neighboring components before generic token handling, and non-native text, terse, and JSON-mode operational errors bypassed `cireport.Operational` entirely.
- The shared sanitizer now sorts and replaces complete configured roots, converts boundary-valid prefix collisions and remaining Unix, drive-letter, and UNC absolute tokens to canonical markers, preserves punctuation and URLs, and only then applies public text cleanup and bounds.
- Focused `internal/cireport` and CLI report tests pass for text, JSON, terse, SARIF, JUnit, and GitHub boundaries, including prefix collisions, Unix and Windows separators, drive letters, UNC paths, punctuation, multiple paths, Unicode roots and whitespace, and a one-component home root.

## Deviations
