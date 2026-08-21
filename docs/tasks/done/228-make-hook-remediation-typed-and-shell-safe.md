# TASK 228: Make hook remediation typed and shell-safe

## Why

Hook status currently decides whether installation advice needs `--force` by
searching human-readable `Detail` text for phrases such as `invalid JSON` or
`differs from`. Wording changes can silently produce unusable advice, and
other drift states can require force without matching the phrase list. The
displayed repository argument is wrapped in double quotes while only embedded
double quotes are escaped, so shell metacharacters and Markdown backticks can
change the copied command or its rendering. Remediation must be derived from
typed state and rendered as exact argv for the current platform.

## Acceptance

- Every hook inspection outcome carries an internal typed remediation
  disposition that distinguishes normal install, forced managed repair,
  non-forceable user-owned conflict, host-specific action, and no action.
- `finalizePlatformStatus` never parses `Detail` or any other display text to
  select behavior.
- `--force` is shown if and only if that exact install path requires and permits
  forced repair; advertised remediation succeeds in an isolated fixture or is
  explicitly marked as manual when automatic repair is prohibited.
- Remediation command data is represented as program plus argv before
  rendering. Quoting round-trips spaces, quotes, dollar signs, backticks,
  semicolons, backslashes, newlines, and platform path separators without
  execution or Markdown injection.
- POSIX and Windows rendering contracts are explicit and tested with the
  parser appropriate to each supported shell surface; no string is described
  as universally shell-safe when it is not.
- Existing `PlatformStatus` JSON fields and meanings remain compatible unless
  a schema-backed additive field is explicitly justified and propagated.
- Tests cover every platform registry entry and every degraded/shadowed state,
  including malformed managed config, generator drift, disabled activation,
  legacy paths, foreign content, missing wrappers, and host-specific repair.
- Hook installation never overwrites user-owned material merely because status
  recommended `--force`.

## Sub-Tasks

- [x] Model typed remediation dispositions and argv at inspection time
- [x] Populate the disposition in every hook status branch
- [x] Replace detail-substring behavior with one remediation builder
- [x] Implement platform-exact command and Markdown rendering
- [x] Add adversarial-path, state-matrix, and executable-remediation tests
- [x] Update hook UX documentation and run registry/portable gates

## Notes

- Session findings: `#24` and `#25`.
- Primary code: `internal/hooks/status.go`, platform-specific hook inspectors,
  and `internal/cli/hook_cmd.go`.
- Existing host-specific remediation that is already set before finalization
  remains authoritative, but it should use the same structured rendering path
  when it presents a command.
- The objective is correct operator guidance, not a generic shell-escaping
  library for unrelated CLI output.
- Status now carries one internal `remediationPlan` with a typed disposition
  and optional program-plus-argv command. Human `Detail` text has no control
  role, while the public JSON `remediation` field remains a string.
- Managed drift uses a normal idempotent install. Malformed merge JSON and an
  explicit Codex `features.hooks=false` use forced repair. Foreign artifacts,
  foreign shared wrappers, invalid activation syntax, and ambiguous Kilo
  legacy duplicates remain manual and never advertise automatic replacement.
- POSIX commands round-trip adversarial argv through `/bin/sh`; PowerShell uses
  literal single-quoted arguments and has a native Windows child-process
  round-trip test. Dynamic Markdown fences cannot be closed by backticks in a
  repository path.
- Verification: complete `internal/hooks` tests, the typed state matrix,
  hook-status CLI integration tests, focused race tests, and `go vet` for hooks
  and CLI pass. Advertised normal and forced repairs are executed in isolated
  fixtures and reach configured status.

## Deviations

None.
