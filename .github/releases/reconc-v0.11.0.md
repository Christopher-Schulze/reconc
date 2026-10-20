# reconc v0.11.0

Reconc 0.11.0 closes the fail-open paths found since 0.10.3, keeps long and
concurrent sessions from locking the repository, retains complete command
evidence, makes the hook hot path and Stop faster, upgrades existing hook
installs without false drift, shows the progress of `reconc update`, and
corrects the 0.10.3 guarantees.

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
- A Stop blocked because the repository changed during evaluation names the
  changed paths, so a check that writes into the repository can be found.
- A JSON `null` host configuration no longer breaks hook installation, and a
  failed JSONL append no longer leaves a torn record.

## Evidence

- Command evidence retains the complete command text as a content-addressed
  object instead of a bounded prefix, and every policy observation resolves it
  before matching.
- Staged evidence is bound to the candidate it was captured for.

## MCP gateway and assurance

- A client that sends an empty or `null` `_meta` with `tools/call` is no
  longer blocked as `protocol_error`.
- A downstream catalog refresh that changes no tool contract keeps the
  published generation, so in-flight calls, pending approvals, and progress
  streams stay valid.
- Drift found immediately before dispatch reports `policy_stale`,
  `tool_contract_stale`, `identity_unavailable`, or the cancellation instead of
  `state_unavailable`.
- Post-dispatch progress no longer re-hashes the downstream executable for
  every event; dispatch still verifies it.
- `applicable_if` globs skip `.git`, `.reconc`, and `node_modules` unless a
  pattern names them, so dependencies neither activate a gate nor exhaust the
  walk budget.

## Performance

- A `require_script` rule runs once per distinct argument and capture set;
  Stop observes Git status only at evaluation barriers; repeated evaluation,
  path normalization, and state work is removed.
- A hook request resolves each recorded path once and only revalidates it
  afterwards: the read-path filter and every policy check share one path
  resolver, confirmed-missing directories are not walked again, existing files
  take their identity from their revalidated directory, and macOS reads a
  directory listing instead of opening each file for its stored spelling. A
  pre-tool call in a Claude Code session with 1,000 recorded reads, 200 writes,
  and 200 commands took 18 ms instead of 85 ms on an Apple M1.
- A pre-tool call of a host whose later events cannot repeat its call id skips
  the decision cache, and a one-shot hook verifies the evidence chain once:
  a Claude Code pre-tool call over 16 maximal evidence segments took 259 ms
  instead of 752 ms.
- A pre-tool call no longer rewrites an unchanged session state or takes the
  active-session lock when the pointer is current, post-tool handlers decode
  the hook payload once, and size-checked files are read with one exact
  allocation.
- Stop builds its cache fingerprint (Git index entries, dirty-file hashes,
  evidence serialization) only for a plan it can cache and looks a cached
  entry up before hashing the evidence: a Stop under a `require_script`
  without `cache_inputs` and 64 dirty files took 31 ms instead of 70 ms.
- Project state lookup no longer lists every ancestor directory on macOS and
  Windows, which cost about 13 ms per hook under a large temporary directory.
- Native assurance keeps a separate scan budget per rule while sharing file
  facts, so a rule's outcome no longer depends on rule order.
- Action-ledger chain verification takes one identity lease for the whole
  chain, so a cold checkpoint load over 256 calls allocated 15 MB instead of
  38 MB, and an action-state transition derives its digest once.
- Every Reconc process starts about 3 ms sooner: package-level regular
  expressions, the command catalog, and the JSON Schema draft metaschemas
  compile on first use instead of at startup. The metaschemas come from an
  in-repository copy of `jsonschema` v6.0.3 that defers only that step. A
  Claude Code pre-tool hook process took 9.4 ms instead of 12.6 ms, and
  `reconc --version` 6.6 ms instead of 9.9 ms.
- A policy check resolves recorded read evidence once, dropping absolute reads
  outside the repository in the same pass: a pre-tool call with 1,000
  recorded reads took about 19 ms instead of 23 ms under load.
- Session state, evidence segments, complete command texts, and Stop reports
  are published with ordered barrier syncs on macOS (`F_BARRIERFSYNC`)
  instead of full device-cache flushes; a power failure can lose their newest
  version but never keeps a state whose segments were lost. A Claude Code
  post-tool Bash call took 3.4 ms instead of 10.7 ms in process. The
  pre-decision cache, which is only a shortcut, skips both syncs: a pre-tool
  call that misses it took about 12 ms instead of 20 ms.
- Stop walks an untracked directory once for its metadata and content
  identity and reuses its hash buffers: a Stop over 1,000 untracked files
  allocated 16 MB instead of 52 MB.
- On macOS, Git subprocesses run the developer-directory git that the
  `/usr/bin/git` xcrun shim would select instead of the shim, which spent
  about 10 ms on every call. A Stop with four Git inspections took 26 ms
  instead of 54 ms.
- The repository hook wrapper runs `uname` only when a repo-local binary
  directory exists, so a repository served by a global installation reaches
  the binary without that subprocess: a wrapped pre-tool call took 16.2 ms
  instead of 18.0 ms.

## Installation and upgrade

- `reconc update` shows each phase (installation, release, skill, download,
  provenance, smoke test, publish) as it completes. On a terminal the running
  phase animates in place with its elapsed time and the download shows a byte
  progress bar; piped output stays plain. The summary states the version
  change, owner, and skill state, and lists only checks that need attention.
- `reconc update` downloads, attests, and smoke-tests the candidate before it
  takes the installation lock, so `doctor --global`, installs, and hooks that
  read the receipt are not blocked meanwhile; publication rechecks the receipt
  under the lock. A binary download ends only with the command or after one
  minute without data instead of failing after two minutes on a slow link.
- `reconc doctor --global` no longer emits the unconditional
  `skill-discovery` warning, names the repair when the owned skill was
  modified or removed, and update reports no longer repeat the pre-update
  comparison of the skill with the running binary.
- A skill-only update no longer copies the whole binary into a backup it never
  uses, so it also works with a read-only binary directory.
- An installation without a receipt-owned skill, such as one installed with
  `--no-skill`, now updates its binary instead of refusing. The skill is only
  proposed as `reconc install-cli --skill-only`, and an unmanaged skill
  directory stays unchanged.

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

- `THIRD_PARTY_NOTICES.txt` and the SBOMs name the in-repository `jsonschema`
  copy as a modified copy of v6.0.3; the notice generator still rejects every
  module replacement that does not resolve inside the repository.
- The host contract source watch digests the contract sections of each
  official source, so unrelated page edits no longer fail it.
- Benchmark peak RSS is measured per benchmark process, and each sample keeps
  the lowest peak of its repeated fixed-work processes, so a one-off
  garbage-collection spike no longer fails the gate.
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
unchanged. Hook artifacts written by any published release since 0.7.0 are
replaced as upgrades without reporting user edits.

Use the installation's existing owner. For direct installations:

```bash
reconc update
reconc doctor --global
```

After updating the binary, use the documented repository synchronization
workflow to refresh generated integrations in existing repositories.

## Delivery

The tag-bound release workflow verifies immutable schema publication,
LangChain MCP interoperability, source and race-test gates, release trust, the
legacy hook generator registry, and isolated self-hosting before building the
platform artifacts. It verifies the canonical manifests, checksums, and remote
asset inventory before publication. Published artifacts include
build-provenance attestations. Windows artifacts remain included; optional
native Windows smoke is disabled by default.
