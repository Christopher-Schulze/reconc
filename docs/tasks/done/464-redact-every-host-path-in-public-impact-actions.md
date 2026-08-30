# TASK 464: Redact every host path in public impact actions

## Why

The public Impact Lab comparison sanitizes action text one whitespace token at a time and replaces at most one absolute-path-shaped candidate per token. Composite diagnostics such as `prefix:/private/one,/private/two` can therefore retain host paths in public JSON and rendered reports.

## Acceptance

- Public Impact Lab action text cannot retain POSIX or Windows absolute paths regardless of surrounding punctuation or multiple paths in one token.
- Repository-relative text and ordinary punctuation remain stable.
- Redaction remains deterministic, UTF-8 safe, and bounded.
- Adversarial regression tests reproduce the current leak and cover composite POSIX and Windows path forms.
- Focused tests and every short repository gate pass; intentionally deferred broad suites are recorded explicitly.

## Sub-Tasks

- [x] Reproduce the composite-token host-path leak with adversarial tests.
- [x] Replace every absolute path span without disturbing safe action text.
- [x] Update the public Impact Lab privacy guarantee and run verification gates.
- [x] Archive, commit, and verify the clean worktree.

## Notes

- Verified from OMP session `01a04db2-0312-77cb-b45b-a22a578cd0d2`, finding 163, during the exhaustive 215-finding coverage reconciliation.
- The current sanitizer only recognizes a token whose trimmed first character is `/` or whose first three bytes form a Windows drive prefix. Embedded and subsequent absolute paths remain untouched.
- The focused regression failed before the fix for composite POSIX, Windows drive, UNC, and Unicode-adjacent path spans while the safe URL/relative-path controls passed.
- The replacement scanner follows the existing CI-report boundary model, preserves URLs and repository-relative `./` paths, and counts every replaced span.
- `go test ./internal/impactlab -count=1`, focused `go vet`, focused Staticcheck, `make fmt-check`, full `make vet`, full `make lint`, and `git diff --check` passed.

## Deviations

- `make test-fast` was stopped after 104.867 seconds in `internal/audit` because the user explicitly required long broad runs to be aborted; formatting, generated-reference checks, and all packages completed before that point were green. `make test` was not run because it unconditionally includes the race and release-trust suites, which the user explicitly reserved for special requests. No local Windows suite was run.
