# Development notes

Unversioned development source. Tag creation and release publication require
separate explicit authorization. These notes describe the changes implemented
since the published `reconc-v0.10.3` release and do not establish a published
release.

## Enforcement fixes

- Literal shell write targets (redirections, `tee`, `cp`, `mv`, `install`,
  `ln`, `touch`, `truncate`, `rm`, and `sed -i`, including nested shells) meet
  write policy before the command runs; dynamic targets keep after-execution
  enforcement. `xargs` option clusters are parsed, so a literal utility behind
  them is analyzed.
- Environment-assignment prefixes no longer make an arbitrary command count as
  read-only.
- Claude Code's PowerShell tool and GitHub Copilot's Windows shell are
  translated for analysis and gated like Bash; dynamic PowerShell forms fail
  closed.
- An inherited `DEVIN_PROJECT_DIR` no longer skips the Claude Code, Codex, and
  Cursor gates. A compatibility route defers to a first-class route only when
  that route's generated matchers cover the tool.
- Claude Code gating hooks declare `onFailure: "block"`, Stop failures no
  longer end the turn, and a settings change that would switch Reconc off is
  blocked through `ConfigChange`. A session inside a linked worktree is
  evaluated in that worktree when its policy keeps every project rule.
- Codex, Cursor, Oh My Pi, Pi, DeepSeek Harness, OpenCode, and Antigravity
  adapters follow their current upstream contracts, including Pi's awaited
  `agent_before_settle` boundary and Antigravity's `run_command` working
  directory.
- Git gates see submodule bumps, keep Git diagnostics out of porcelain records,
  and judge `git commit -a` and pathspec commits against the commit index.
  Staged CI evidence is bound to the fingerprinted candidate, also for nested
  policy roots.
- Composite `deny_write` paths expand `{var}` captures, and an unwritten hook
  decision no longer allows the call.
- The repository wrapper pins the host binary, and `reconc hook status`
  degrades when the wrapper resolves no runnable binary.
- Operator administration (`hook pause`, `hook evidence-resolve`, and
  `operator:*` claims) requires a local interactive confirmation in addition
  to the pre-tool refusal of recognized agent invocations. `hook resume` only
  restores enforcement and needs no confirmation.

## Session reliability

- Evidence-segment rotation, oversized session state, and long sessions no
  longer lock the repository; concurrent session evidence survives retention.
- CRLF and large TASK boards, concurrent writes during a board read, and
  transient board races are retried or parsed instead of blocking Stop.
- A JSON `null` host configuration no longer breaks hook installation, and a
  failed JSONL append no longer leaves a torn record.

## Evidence

- Command evidence retains the complete command text as a content-addressed
  object instead of a bounded prefix, and every policy observation resolves it
  before matching.
- Staged evidence is bound to the candidate it was captured for.

## Performance

- A `require_script` rule runs once per distinct argument and capture set;
  Stop observes Git status only at evaluation barriers; repeated evaluation,
  path normalization, and state work is removed.
- A pre-tool call of a host whose later events cannot repeat its call id skips
  the decision cache, and a one-shot hook verifies the evidence chain once:
  a Claude Code pre-tool call over 16 maximal evidence segments took 394 ms
  instead of 752 ms on an Apple M1.
- Project state lookup no longer lists every ancestor directory on macOS and
  Windows, which cost about 13 ms per hook under a large temporary directory.
- Native assurance keeps a separate scan budget per rule while sharing file
  facts, so a rule's outcome no longer depends on rule order.

## Installation and upgrade

- Hook install and uninstall recognize the exact generator output of every
  published release since 0.7.0, so an upgrade replaces it without reporting
  user edits and uninstall removes it; edited entries are still reported or
  left in place.
- `reconc init` and `bootstrap apply` never replace a valid direct release
  installation. The release installers pass their verified artifact digest,
  so `install-cli` reuses that attestation instead of calling `gh` again.
- Installer environment variables can no longer record `github-verified` for
  a binary that `gh` did not verify, shadowed reinstalls are refused, and an
  allowed downgrade still refuses a release that cannot read the installation
  receipt.

## Maintenance

- The host contract source watch digests the contract sections of each
  official source, so unrelated page edits no longer fail it.
- Benchmark peak RSS is measured per benchmark process.
- Tests stage one shared binary, isolate host settings, and protect the recent
  fixes with failing-first regression tests.

## Corrections to reconc-v0.10.3

The `reconc-v0.10.3` notes overstated several guarantees:

- It did not remove every way a governed session could seal itself shut:
  evidence-segment rotation, oversized session state, and CRLF or concurrently
  written TASK boards could still block a session. These paths are fixed above.
- Its bounded command prefix dropped trailing command text from the evidence
  that policy matched. Evidence now retains the complete text.
- `hook evidence-resolve` resets the resolved session's overflowed evidence
  window; that session continues from an empty window, and required evidence
  must be reproduced.
- Agent tool calls could still run operator-only commands through scripts,
  aliases, or copied binaries that the pre-tool guard did not recognize. The
  guard refuses recognized invocations, and the commands now also require a
  local interactive confirmation; a process with the same user rights can
  still automate a terminal.
- It shipped fail-open paths that are closed above: arbitrary commands behind
  environment-assignment prefixes, an inherited `DEVIN_PROJECT_DIR`, hidden
  submodule bumps, Git diagnostics in porcelain records, literal `{var}` in
  composite `deny_write` paths, an unwritten hook decision, commits judged
  against the wrong index, an ungated Claude Code PowerShell tool, and Claude
  Code gating hooks without `onFailure: "block"`.

## Compatibility And Upgrade

No policy-schema or lockfile-format change is introduced, and no manual data
migration is required. Existing immutable schema publication identities remain
unchanged. After updating the binary, run the documented repository
synchronization workflow; hook artifacts written by a published release are
replaced as upgrades.
