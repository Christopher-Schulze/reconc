# TASK 388: Harden CLI value parsing and helper implementations

## Why

The shared `nextArgValue` consumes the next token even when a flag that requires a non-flag value is followed by another option. The same helper file carries hand-written sorting, splitting, joining, integer, and first-element utilities already covered by standard-library contracts.

## Acceptance

- Every value-taking option declares whether leading-dash values are legal.
- Missing values report the owning option and never consume a following option accidentally.
- Command/evidence values that legitimately begin with `-` remain supported through an explicit syntax or typed parser contract.
- Standard-library replacements preserve exact formatting, ordering, nil, and empty behavior; table tests cover all 73 current call sites.
- Shell-mode `reconc exec` does not render the direct-command form that it immediately discards.

## Sub-Tasks

- [x] Classify every `nextArgValue` caller by value grammar.
- [x] Introduce the smallest typed parsing contract and migrate callers atomically.
- [x] Replace only behavior-equivalent helper reimplementations.
- [x] Remove the verified shell-mode `renderDirectCommand` allocation without changing recorded command text.
- [x] Run focused CLI parser tests and full CLI package tests.

## Notes

- Verified from findings 30, 31, and 32 plus worker finding 702.
- A blanket `strings.HasPrefix(value, "-")` rejection is incorrect because some command and evidence values may legitimately start with a dash.
- Current source has 73 call expressions across 22 caller files; the original count of 74 included the `nextArgValue` declaration itself. Fifty-four calls accept only ordinary values, while 19 shared evidence/command calls support a leading dash only through an explicit separator escape.
- Focused parser/helper tests and the complete `internal/cli` package pass after migration.
- `make test-fast` passes for both Go modules, including formatting and generated-reference checks.

## Deviations
