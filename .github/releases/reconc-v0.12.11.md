# reconc v0.12.11

Reconc 0.12.11 keeps Stop bounded and able to end when it cannot be evaluated,
stops refusing copies onto existing files, and ties an approval quote to its
own claim grant.

## Stop

- A Stop whose policy inputs keep moving, or whose policy source is invalid,
  holds once and names the cause, then ends as `uncertified_termination`. It is
  never reported as clean, grants no authority, leaves the commit gate in force,
  and enters the run log when repository run is enabled. Strict continuation
  hosts stay held. Pending background work defers the verdict to a later Stop.
- The complete encoded block response stays within 4,095 bytes, half of the
  route's output budget less its frame, including after JSON escaping. A report
  path that cannot fit is named as omitted instead of being shortened.
- The report path, feedback ID and run status come first, so they survive on
  hosts that apply the 1,024-byte reason limit.
- Stop and lockfile diagnostics are bounded and never split a UTF-8 sequence.
- The moving-inputs message no longer asserts a concurrent writer: a check that
  rewrites its own inputs produces the same observation.

## Shell write targets

- `cp`, `mv`, `install` and `ln` onto an existing regular file no longer fail
  with "not a directory" before the command runs. A path below a destination
  counts only unless that destination exists as a non-directory.
- Protected targets stay blocked, including through file and directory symlinks.

## Operator claims

- `hook claim --user-approved --approval-quote TEXT` adds the user's words to the
  attestation for review. The quote authorizes nothing by itself.
- A quote belongs to one grant: claim and quote are stored in one update, and
  recording the claim again replaces the quote or clears it.

## Maintenance

- Reviewed host contract baselines for Claude Code, Codex, Devin CLI and
  OpenCode; no adapter change was required.
- The repository boundary error now says that work in another repository needs
  its own session there.

This release was published without the optional race, Vet, Staticcheck and
Govulncheck gates at the operator's request. CI, CodeQL and the release trust
gate apply.
