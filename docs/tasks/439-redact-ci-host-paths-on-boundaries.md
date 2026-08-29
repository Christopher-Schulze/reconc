# TASK 439: Redact CI host paths on boundaries

## Why

CI operational-error redaction performs raw substring replacement for repository and home paths. Neighboring path components can be partially rewritten, producing misleading output and leaving user/host fragments outside a canonical path token.

## Acceptance

- Repository and home replacements match complete path components or validated absolute-path tokens on Unix and Windows.
- Neighbor paths and ordinary text containing the same substring are not corrupted.
- No absolute host path, home fragment, or username-bearing path survives rendered JSON, JUnit, SARIF, or text output.
- Adversarial tests cover prefix collisions, drive letters, separators, punctuation, multiple paths, Unicode, and short home names.

## Sub-Tasks

- [ ] Define one boundary-aware host-path tokenizer for CI reports.
- [ ] Apply it before public text bounding without exposing raw paths.
- [ ] Add renderer-level privacy regressions.
- [ ] Run focused CI report tests.

## Notes

- Verified from finding 166 in `internal/cireport/model.go`.

## Deviations
