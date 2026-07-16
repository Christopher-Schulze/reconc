# reconc -- Command Reference

Full reference for all 41 subcommands. See `reconc <subcommand> --help` for
the exact flag details emitted by the installed binary.

## Daily path

Use this first:

```bash
reconc bootstrap .
reconc status .
reconc check . --write path/to/file
reconc next .
reconc done .
```

Everything below is the full automation and diagnostic surface.

## Exit codes

- `0` pass / warn / informational success
- `1` runtime or input error
- `2` at least one blocking policy violation

## Environment

Runtime:

- `RECONC_HOME` (default `~/.reconc`) -- user config, presets, templates
- `RECONC_AUDIT=1` -- enable the opt-in append-only audit log
- `RECONC_AUDIT_VERBOSE=1` -- store full command strings in audit records
  instead of the redacted first token (may capture secrets in arguments)
- `RECONC_CLAUDE_STATE_DIR` -- override the global session-state root
- `RECONC_SCHEMA_BASE_URL` -- enterprise override for schema URLs; defaults to the format-versioned repository contracts under `schemas/v1/`
- `RECONC_STOP_FINGERPRINT_UNTRACKED` (`normal` default, `all`, `no`) --
  untracked-file mode for the Stop fingerprint's git status snapshot

Debugging:

- `RECONC_HOOK_TIMING=1` -- print per-stage hook-runtime timings to stderr
- `RECONC_HOOK_TIMING_THRESHOLD_MS` -- only print timings above this bound
- `RECONC_AUDIT_NO_CACHE=1` -- bypass the audit stats cache

Installer (`install.sh` only):

- `RECONC_INSTALL_DIR` (default `/usr/local/bin`) -- install target
- `RECONC_RELEASE_BASE` -- release download mirror
- `RECONC_REQUIRE_ATTESTATION=1` -- make GitHub provenance verification
  of `SHA256SUMS` mandatory
- `RECONC_ATTESTATION_TOOL` / `RECONC_ATTESTATION_REPO` -- override the
  verification tool (default `gh`) and repository

Variables prefixed `RECONC_HOOK_` other than the timing pair (for example
`RECONC_HOOK_REPO_RESOLVED`, `RECONC_HOOK_RUNTIME`) are internal wrapper
plumbing, not user configuration.

---

## Bootstrap & inspection

### `reconc init [repo] [--preset NAME] [--force] [--output PATH]`
Scaffolds `.reconc.yml` + a stub `AGENTS.md` for a fresh repo. Multiple
`--preset` flags compose. Refuses to overwrite existing files unless
`--force` is set. `--output` mirrors the primary text or JSON output
to a file while still printing to stdout.

### `reconc bootstrap inspect [repo] [--json]`
Read-only discovery of canonical repository root, detected Go/Bun stacks,
review-only pack suggestions, detected agent-platform directories, existing
control paths, and platform-correct repo-local binary resolution.

### `reconc bootstrap profiles [--json]`
List the three explicit profiles. `minimal` selects policy plus a managed AI
orientation block. `governed` adds the TASK control plane, documentation,
`start.md`, runtime ignores, and the stable hook wrapper. Both default to the
`default` and `agent` packs. `existing` owns only selected hooks, the wrapper,
and an optional stable binary. It requires an already fresh compiled policy,
accepts no packs, and never owns existing control-plane files.

### `reconc bootstrap plan [repo] --profile existing|minimal|governed [--pack NAME] [--hook KIND] [--install-binary | --binary PATH --checksum SHA256 [--platform OS/ARCH]] [--output PATH] [--json]`
Build a deterministic, versioned manifest of desired hashes, modes, current
state, conflict candidates, compilation need, and blocking issues. Packs and
hooks are repeatable explicit selections; detected suggestions are never
applied automatically. The command is read-only unless `--output` is supplied.
Plan files are create-only and an exact repeat is reported as unchanged.

### `reconc bootstrap apply --plan PATH [--json]` / `reconc bootstrap apply [repo] --profile existing|minimal|governed [selection flags] [--json]`
Apply an exact reviewed plan or build the same plan from explicit selections.
Repository targets are create-only. Exact files remain unchanged; any drift
creates hash-addressed candidate files and prevents all normal target installs.
Stale plans fail before publication. Failures roll back only transaction-owned
files whose identity and checksum still match. Status `drift` exits 1.

### `reconc bootstrap verify --plan PATH [--json]`
Read-only verification of every selected artifact hash and mode, candidate
drift, policy-lock freshness, governed TASK structure, selected hook activation,
and selected binary checksum/resolution. Any failed check exits 1.

### `reconc bootstrap [repo] [--preset NAME] [--skip-git-hook] [--skip-agent-hooks] [--json]`
Compatibility shorthand for a create-only `minimal` transaction. It explicitly
selects the git hook when `.git/` exists and selects registered agent hooks only
for detected repo-local platform directories. `--force` is rejected; drift must
be resolved through candidate review.

### `reconc adopt [repo] [--yaml | --json | --apply]`
Detects common tooling (JS/TS, Python, Rust, Go, CI, generated dirs)
and emits matching-rule suggestions. Go and Bun evidence can also produce
review-only manifested policy-pack recommendations. `--apply` appends
individual rules to `.reconc.yml` idempotently and never changes `extends`.

### `reconc extract [repo] [--from PATH] [--yaml | --json]`
Regex-heuristic scan of AGENTS.md / CLAUDE.md prose for concrete rule
hints (don't-edit / generated / run-before-commit / secrets / ci-green
patterns). Emits suggestions in the same format as `adopt`.

### `reconc doctor [repo] [--deep] [--json] [--output PATH]`
Default mode inspects discovery state only. `--deep` adds seven
diagnostic checks: hook-runtime compatibility, native Grok hook trust/loading,
lockfile freshness, audit-log size, preset/template reference resolution, session-claim
age, and static rule conflicts. Deep mode exits 1 when any check is
`FAIL`, 0 when all rows are `OK` or `WARN`.

### `reconc verify [repo] [--json]`
End-to-end installation health check: PATH, `$RECONC_HOME`, presets, repo
discovery, read-only policy parsing, lockfile freshness, git
pre-commit hook, and agent-hook runtime compatibility. Always exits 0;
WARN rows flag optional misses.

### `reconc status [repo] [--json] [--output PATH]`
One-line, read-only policy health summary. Missing, stale, malformed,
schema-drifted, migration-drifted, and wrong-root lockfiles surface as issues
with explicit `reconc refresh .` remediation. Useful as a session-start ping.

### `reconc done [repo] [--window N] [--require-clean-git] [--json]`
Terse task-finish gate. Prints `done` when the lockfile is present,
fresh enough for the known audit window, and no recent blocking audit
entry exists. Prints `blocked: <next action>` and exits 2 when the task
is not ready. `--require-clean-git` also requires a clean working tree.

---

## Compile & evaluate

### `reconc compile [repo] [--json] [--strict-conflicts] [--output PATH]`
Produces `.reconc/policy.lock.json` from sources. With
`--strict-conflicts`, exits 1 when any rule conflict is detected. A
`forbid_command` conflicts with `require_command` only when their exact trigger
scopes overlap and the forbid rule blocks every acceptable required command;
one blocked option among several valid alternatives remains satisfiable.

### `reconc refresh [repo] [--json] [--strict-conflicts] [--output PATH]`
Explicit policy refresh. Uses the same deterministic compiler pipeline as
`compile` and is the canonical remediation emitted by read-only commands.

### `reconc check [repo] [--read PATH] [--write PATH] [--command CMD] [--command-success CMD] [--command-failure CMD] [--claim NAME] [--auto-claim] [--json] [--terse] [--output PATH]`
The core policy evaluator. Exit 0 = pass/warn, 2 = block, 1 = error.
`--terse` emits ~50-token JSON optimised for hook-loop calls.
`--auto-claim` detects CI environment and auto-asserts `ci-green`.
Missing or stale lockfiles fail closed without writing and require
`reconc refresh .`.

### `reconc ci [repo] (--staged | --base REF [--head REF]) [--read PATH] [--command CMD] [--command-success CMD] [--command-failure CMD] [--claim NAME] [--auto-claim] [--json] [--output PATH]`
Git-aware check. Derives write paths from the working-tree index or a
`base..head` range instead of explicit `--write` flags. It inherits recorded
read paths, commands, command results, and claims from the active agent session,
so pre-commit evaluates the same evidence without repeating warnings.
Missing or stale lockfiles fail closed without writing and require
`reconc refresh .`.

### `reconc assert <rule-id> [repo] [--var K=V] [--read PATH] [--write PATH] [--command CMD] [--claim NAME] [--json]`
Evaluate exactly one rule, ignoring the rest of the lockfile. Useful
for single-rule workflows and template-variable rule tests.

### `reconc can <action> <path> [repo] [--why] [--json]`
Ultra-terse yes/no. Prints `yes` or `no: <rule> <action>`. Exit 0 =
yes, 2 = no, 1 = error. Action is currently always `write`.

### `reconc diff <lockfile-a> <lockfile-b> [--json]`
Structural comparison of two compiled lockfiles. Reports added /
removed / changed rules and default-mode / source-digest drift.
Ignore-provenance semantics: relocating a rule between source files
doesn't register as a change.

### `reconc watch [repo] [--interval-ms N]`
Poll sources every N ms (default 800) and recompile on any mtime
change. Runs until Ctrl-C.

---

## Explain & remediate

### `reconc explain [repo] [--read PATH] [--write PATH] [--command CMD] [--claim NAME] [--format text|markdown] [--json] [--output PATH]`
Render the check report in human-readable form. Source can be fresh
inputs or a saved `CheckReport` JSON.

### `reconc fix [repo] [--read PATH] [--write PATH] [--command CMD] [--claim NAME] [--json] [--next] [--output PATH]`
Structured remediation plan per violation, with per-kind steps,
suggested commands / claims, and files-to-inspect. `--next` emits only
the top-priority remediation.

### `reconc next [repo] [--read PATH] [--write PATH] [--command CMD] [--claim NAME] [--json]`
Friendly alias for `fix --next`. Prints only the highest-priority next
action, so agents can ask for guidance without loading the full fix
plan.

### `reconc why <rule-id> [repo] [--json] [--terse]`
Prints the full rule from the lockfile (kind, mode, message, paths,
provenance, DEPRECATED label if set). `--terse` emits only kind, mode,
first path, and a shortened message.

---

## Packs & wiring

### `reconc preset list [--json] [--output PATH]` / `reconc preset show <name> [--json] [--output PATH]`
Built-in (`default`, `agent`, `docs-sync`, `release`, `strict`,
`go-assurance`, `bun-assurance`) + user presets from
`$RECONC_HOME/presets/*.yml`. User-authored presets override bundled
ones on name collision. JSON listing includes each validated manifest and its
declared capabilities when present.

### `reconc template list [--json]` / `reconc template show <name> [--json]`
Rule shape templates (`tests-follow-source`, `docs-follow-code`,
`no-generated-writes`, `ci-green-before-merge`). User overrides in
`$RECONC_HOME/templates/*.yml`.

### `reconc hook generate <git-pre-commit|claude-code|codex|cursor|opencode|devin-cli|antigravity|kilo|grok> [--json] [--output PATH]`
Emit the hook artefact content without writing to disk.

### `reconc hook install <git-pre-commit|claude-code|codex|cursor|opencode|devin-cli|antigravity|kilo|grok> [repo] [--force] [--json] [--output PATH]`
Write the hook into the repo. Git pre-commit reuses an identical managed
`.git/hooks` file and refuses different content without `--force`; Claude Code
and Codex JSON configs are merged non-destructively;
Cursor writes `.cursor/hooks.json`; OpenCode writes
`.opencode/plugins/reconc.js`; Devin merges `.devin/hooks.v1.json`;
Antigravity merges the top-level
`reconc` hook definition into `.agents/hooks.json`, preserving
non-reconc hook groups; and Kilo Code owns
`.kilo/plugin/reconc.js`. Grok Build owns the dedicated
`.grok/hooks/reconc.json` file and preserves every other project hook file.
Managed plugin/files refuse unrelated existing
content unless `--force` is passed.

### `reconc hook status [repo] [--json]`
Validate registered artifacts and activation requirements. States are
`absent`, `installed`, `configured`, `degraded`, `shadowed`, and `unsupported`.
The command checks malformed, incomplete, non-executable, or drifted managed
artifacts, the repo-local wrapper, Codex's enable flag, Git `core.hooksPath`,
Kilo Code pure mode, legacy Kilo Code plugin placement, and Grok's native
project-hook artifact. Static Grok status cannot prove folder trust; `doctor
--deep` additionally runs `grok inspect --json` when the artifact exists.
Each platform also reports rate-limited `last_seen`/`last_event` live-runtime
evidence separately from static activation state. `configured` proves only
that the host can discover a complete static artifact. Codex accepts
`hooks = true` under `[features]`, rejects root-level `hooks=true`, and has no
`SessionEnd` route. OpenCode and Kilo Code continuation is inferred from
`session.idle`, not a synchronous native Stop gate. Grok's native `Stop` event
is passive; use `reconc grok` when hard automatic continuation is required.

### `reconc hook sync-scaffold <repo-root-scaffold> [--json]`
Regenerate source-controlled hook artifacts inside a template
`repo-root-scaffold`: `.githooks/pre-commit`, `.codex/hooks.json`,
`.cursor/hooks.json`, `.agents/hooks.json`, `.claude/settings.json`,
`.opencode/plugins/reconc.js`, `.devin/hooks.v1.json`,
`.kilo/plugin/reconc.js`, and `.grok/hooks/reconc.json`. This keeps scaffolded repos on the
same generator truth as `reconc hook install`; do not copy these files
from a source-specific harness.

### `reconc hook claim <repo> <claim-name> [--json] [--output PATH]`
Assert a workflow claim (e.g. `ci-green`). Written to the session
state consulted by later hook-runtime checks and `ci` calls.

### `reconc hook runtime <event> <repo>`
Registry-owned agent-platform event dispatcher. Called from Claude Code,
Codex, Cursor, OpenCode, Devin CLI, Antigravity CLI, Kilo Code, and Grok Build
hook configs, not by users directly.

### `reconc grok [repo] [--model ID] [--grok-binary PATH] [--max-continuations N] --prompt TEXT`
Starts the unmodified official `grok agent stdio` ACP runtime in the target
repository. Preflight requires the generated `.grok/hooks/reconc.json`, the
repo-local wrapper, project trust, and a live `grok inspect --json` report that
contains every native route. The driver streams Grok's answer and re-prompts
the same ACP session while Reconc's strict Stop evaluation returns a
continuation reason. Ctrl-C terminates immediately. The default continuation
limit is 32. ACP uses Grok's `--always-approve` transport because it has no TUI
permission modal; Reconc PreToolUse and Grok's explicit deny rules still run.

---

## Workflow maintenance

### `reconc changelog rotate [repo] [--force] [--lines N] [--json]` / `reconc changelog list-archives [repo] [--json]`
Rotate `docs/changelog.md` when it exceeds the line threshold (default
200). Moves older `##`-sections into
`docs/changelog/archive/YYYY-QN.md`. Idempotent.

### `reconc agent-intro [--section NAME] [--list-sections] [--json]`
Prints the embedded reconc integration guide. Section lookup is
case-insensitive substring match.

### `reconc audit tail [repo] [-n N] [--rule ID] [--since RFC3339] [--decision pass|warn|block] [--json] [--compact]`
Tail the decision log. Filters combine. `--compact` emits
`<ts> <event> <decision> <rule_id>`.

### `reconc audit stats [repo] [--json]`
Aggregate summary: totals, latest decision and blocking count, last-hour
activity, blocking events in the last 24 hours, by-decision, by-event, and top
rules.

### `reconc audit export [repo]`
Raw JSONL dump on stdout for external tooling. Audit tail, stats, and export
read the two bounded archives plus the live file in chronological order.

### `reconc run on [repo] [--json]` / `reconc run off [repo] [--json]`
AI-operated switch scoped to one repository, not the whole machine. It routes
continuation through all eight registered agent runtimes. Claude Code, Codex,
Cursor, Devin CLI, and Antigravity CLI expose synchronous Stop
gates; OpenCode and Kilo Code use inferred `session.idle` adapters whose host
boundary is best-effort and fail-open. Grok's stock TUI exposes only a passive
Stop notification; `reconc grok` supplies strict same-session continuation
through the official ACP runtime. Typed `continue` and `claim` states
continue: `Current: none` or an empty Active section still claims queued
executable work. Complete or absent state disables the switch after terminal
gates; blocked state reaches terminal Stop without silently disabling it, and
invalid state fails closed. An explicit interrupt or six repeated no-progress
continuations releases only the current invocation. Ordinary prompts, session
end, runtime changes, and application restarts never mutate the durable switch.
`off` is the only manual disable action. Both commands are idempotent and append
a decision record only when state actually changes. The agent executes these
commands itself; it must not ask the user to operate Reconc.

### `reconc run status [repo] [--json]`
One-line or JSON snapshot of run mode plus typed TASK disposition:
`enabled`, `task_disposition`, current TASK/Sub-Task, open count, no-progress
state, blocker, and reason. Invalid TASK state is reported as disposition
`invalid`; Stop then fails closed with the validation error.

### `reconc run log [repo] [-n N] [--branch B] [--session S] [--follow] [--json]`
Render the bounded run decision ring: material state transitions,
continuations, policy blocks, no-progress releases, explicit switches, and
stop reasons. Disabled no-op events and unchanged state are not logged.
`--branch`/`--session` filter, `-n` keeps the last N, and `--follow` tails new
records until Ctrl-C.

### `reconc task <subcommand>`
Typed repository TASK control with two non-migrating profiles:
`sections-v1` for bounded Active/Queue/Blocked/Done sections and `logbook-v1`
for a `Current:` line plus detail `State:` fields. `Current: none` is the
explicit valid logbook state when no TASK is active. Configure the profile,
overview/detail paths, Done window, and required completion evidence under
`task_lifecycle` in `.reconc.yml`; `auto` succeeds only on an unambiguous exact
grammar match. Explicit configuration makes the overview mandatory.
`completion.require_committed: true` requires the terminal TASK control-plane
changes to be committed, reusing the terminal Stop snapshot without adding Git
work to executable TASK continuations.

- `task status [repo] [--json]`: current TASK, current Sub-Task, bounded blockers, missing configured evidence, exact next action
- `task validate [repo] [--json]`: full live-control-plane validation with stable issue IDs
- `task check-done [repo] [--task ID] [--json]`: fail closed on any unfinished Sub-Task or missing configured evidence
- `task claim <ID> [repo] [--json]`: activate one executable queued TASK
- `task block [repo] --reason TEXT [--next ID] [--json]`: block current and optionally activate a successor
- `task resume <ID> [repo] [--json]`: reactivate a blocked TASK when no TASK is active
- `task split [repo] --children ID,ID [--json]`: block the parent and activate the first pre-created, parent-linked child
- `task promote [repo] [--next ID] [--json]`: completion-check, archive, and activate the next executable TASK
- `task archive [repo] [--json]`: terminal archive for either profile with no queued successor
- `task recover [repo] [--json]`: integrity-check and roll back an interrupted transaction without overwriting external edits

Mutations use `.reconc/locks/task-lifecycle.lock`, atomic publication, verified
renames, and `.reconc/task-transaction.json`. Normal reads never open unlinked
archive history. Briefings cap blockers/evidence and free text; transactions
reject symlinked paths, preserve file modes, and cap journals at 4 MiB.

### `reconc prune [repo] [--dry-run] [--json]`
Run the product retention core immediately. It bounds external session,
report, and lock state; audit and run-decision JSONL rings; generated workflow-audit
binaries; abandoned repo-local atomic/build temps; and owned
`reconc-proof-*` temp trees. `--dry-run` reports file candidates without
deleting them. Owned proof temp trees use a two-hour inactivity grace.
SessionStart and SessionEnd invoke the same core through a
six-hour due check; Stop never prunes. `--force` remains accepted as a no-op
compatibility flag for the former harness utility.

### `reconc session-briefing [repo] [--json]`
Compact delta-oriented session state: current TASK/Sub-Task, bounded blockers,
current policy delta, required evidence, durable repository-run status, saved
report path, and one exact next action. JSON includes `format_version` for
machine consumers. Aggregate audit history and Git are intentionally excluded
from this hot path. It is read-only; missing or stale lockfiles require
`reconc refresh .`.

### `reconc context size [repo] [--limit N] [--files PATH,PATH,...] [--json]`
Guards the auto-loaded session-file token budget (default 20000 tokens).
Without `--files`, it measures `AGENTS.md`, `CLAUDE.md`, `start.md`,
`docs/tasks.md`, and the active TASK detail when present. Custom paths replace
that default. Paths are normalized and deduplicated; lexical and symlink
escapes outside the repository fail closed. Non-empty files round up to at
least one approximate token. JSON includes `format_version`; exit 1 over limit
so CI gates can block budget-growing PRs.

### `reconc start [repo] [--write PATH] [--force] [--json] [--minimal]`
Renders a canonical `start.md` onboarding / reentry doc from the
current state. Reuses session-briefing + audit-tail data. `--minimal`
emits a compact 3-line summary.

### `reconc post-task-check [repo] [--window N] [--require-clean-git] [--json]`
Read-only pre-done gate: valid fresh lockfile + no blocking audit entries in
the last N minutes (default 10). Exit 1 on any check failure.

### `reconc delta [repo] [--since RFC3339] [--json]`
Audit activity since a reference point (default 1h ago), with
decision / event breakdowns.

### `reconc spec check [repo] [--file PATH] [--max-age-days N] [--json]`
Verifies `docs/spec.md` (or `--file`) exists and is fresh. Exit 1 on
missing file or exceeded age.

### `reconc coverage check [repo] [--file PATH] [--min-pct N] [--json]`
Reads the first percentage from a coverage artefact, compares to
`--min-pct` (default 80). Supports XX.X% text, bare numbers, and
`go test -cover` output.

### `reconc tui [repo] [--json] [--output PATH]`
Dependency-free terminal dashboard for policy state. Shows discovery,
lockfile freshness, source list, rule list, audit summary, active
session id, conflicts, and the next action. `--json` emits the same
snapshot as structured data. It never refreshes policy implicitly.

### `reconc completion <bash|zsh|fish>`
Emit a shell completion script. Install one-liners:

```bash
reconc completion bash > /usr/local/etc/bash_completion.d/reconc
reconc completion zsh  > /usr/local/share/zsh/site-functions/_reconc
reconc completion fish > ~/.config/fish/completions/reconc.fish
```

### `reconc manpage`
Emit the roff man page (section 1) on stdout, generated from the same
subcommand table as shell completion. Install example:

```bash
reconc manpage > /usr/local/share/man/man1/reconc.1
```

### `reconc version [--json]`
Print the build version as text or JSON. Equivalent to top-level
`reconc --version`.
