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
- `NO_COLOR` -- disable ANSI styling even when stdout is a terminal; redirected
  and `TERM=dumb` output is always plain
- `RECONC_AUDIT=1` -- enable the opt-in append-only audit log
- `RECONC_AUDIT_VERBOSE=1` -- store full command strings in audit records
  instead of the redacted first token (may capture secrets in arguments)
- `RECONC_CLAUDE_STATE_DIR` -- override the global session-state root
- `RECONC_SCHEMA_BASE_URL` -- enterprise override for schema URLs; without an
  override, config/report/fix-plan contracts use `schemas/v1/` and current
  policy lockfiles use `schemas/v2/`
- `RECONC_STOP_FINGERPRINT_UNTRACKED` (`normal` default, `all`, `no`) --
  untracked-file mode for the Stop fingerprint's git status snapshot
- `RECONC_GROK_STEER=0` -- disable optional Grok TUI leader steering over the
  Unix socket or Windows named pipe; PreToolUse remains enforced and native
  Stop remains available only when the installed Grok guide advertises it
  (steering also honours `GROK_LEADER_SOCKET`)

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
Scaffolds `.reconc.yml`, a stub `AGENTS.md`, and the same marker-owned Reconc
runtime-ignore block used by bootstrap profiles. The compiled lockfile remains
committable. Multiple
`--preset` flags compose. Refuses to overwrite existing files unless
`--force` is set. `--output` mirrors the primary text or JSON output
to a file while still printing to stdout.

### `reconc bootstrap inspect [repo] [--json]`
Read-only discovery of canonical repository root; detected Go, Bun, Python,
Rust, Shell, C/C++, Java, PHP, C#, Next.js, Svelte/SvelteKit, Zig, Elixir, and
PowerShell stacks; evidence paths, package managers, repository markers,
same-directory package-manager ambiguities, review-only pack suggestions;
detected or installed agent platforms with generated/installed/executable/
configured truth, existing control paths, and
platform-correct repo-local binary resolution.

### `reconc bootstrap profiles [--json]`
List the three explicit profiles. `minimal` selects policy, a managed AI
orientation block, and runtime ignores. `governed` adds the TASK control plane,
documentation, `start.md`, and the stable hook wrapper. Both default to the
`default` and `agent` packs. `existing` owns only selected hooks, the wrapper,
and an optional stable binary. It requires an already fresh compiled policy,
accepts no packs, and never owns existing control-plane files.

### `reconc bootstrap plan [repo] --profile existing|minimal|governed [--pack NAME] [--hook KIND] [--install-binary | --binary PATH --checksum SHA256 [--platform OS/ARCH]] [--output PATH [--replace-output]] [--json]`
Build a deterministic, versioned manifest of desired hashes, modes, current
state, conflict candidates, compilation need, and blocking issues. Packs and
hooks are repeatable explicit selections; detected suggestions are never
applied automatically. The command is read-only unless `--output` is supplied.
Plan files are create-only and an exact repeat is reported as unchanged.
`--replace-output` is an explicit stale-plan recovery path: it replaces only a
strictly valid Reconc plan for the same canonical repository and refuses an
arbitrary or cross-repository file.

### `reconc bootstrap apply --plan PATH [--json]` / `reconc bootstrap apply [repo] --profile existing|minimal|governed [selection flags] [--json]`
Apply an exact reviewed plan or build the same plan from explicit selections.
Repository targets are create-only. Exact files remain unchanged; any drift
creates hash-addressed candidate files and prevents all normal target installs.
Stale plans fail before publication and print the full copy-paste
`bootstrap plan ... --replace-output` command reconstructed from the saved
selection. Failures roll back only transaction-owned
files whose identity and checksum still match. Status `drift` exits 1.
Successful reports contain one compact created/preserved/drifted/skipped and
installed/configured/live summary, a tamper-evident receipt path, and exactly
one primary next command. Human output uses TTY-only ANSI color for decisions,
rule IDs, and OK/WARN/FAIL tags; JSON and redirected output never contain ANSI.

### `reconc bootstrap remove --plan PATH [--json]`
Reverse one applied plan using its tamper-evident install receipt. Exact
transaction-owned files are removed; marker-delimited blocks are stripped from
shared files. Drifted shared files produce hash-addressed removal candidates,
all ambiguous content is preserved, and any partial failure rolls back applied
mutations. The shared hook wrapper remains when independent platform hooks may
still use it.

### `reconc bootstrap verify --plan PATH [--json]`
Read-only verification of every selected artifact hash and mode, candidate
drift, policy-lock freshness, governed TASK structure, selected hook activation,
and selected binary checksum/resolution. Any failed check exits 1.

### `reconc bootstrap [repo] [--preset NAME] [--skip-git-hook] [--skip-agent-hooks] [--accept-managed-blocks] [--json]`
Compatibility shorthand for a create-only `minimal` transaction. It explicitly
selects the git hook when `.git/` exists and selects registered agent hooks only
for detected repo-local platform directories. `--force` is rejected; drift must
be resolved through candidate review. When every changed byte is one recognized
marker-owned Reconc block, the first run emits the exact
`--accept-managed-blocks` rerun. That explicit rerun revalidates the unchanged
target and plan-exact candidate, promotes the block transactionally, removes
the candidate, replans, and continues. It never accepts a whole-file or
non-marker conflict.

### `reconc adopt [repo] [--yaml | --json | --apply]`
Detects common tooling (JS/TS, Python, Rust, Go, Shell, C/C++, Java, PHP, C#,
Next.js, Svelte/SvelteKit, Zig, Elixir, PowerShell, CI, generated dirs) and
emits matching-rule suggestions. Stack evidence can
also produce review-only manifested policy-pack recommendations. `--apply`
appends individual rules to `.reconc.yml` idempotently and never changes
`extends`.

### `reconc extract [repo] [--from PATH] [--yaml | --json]`
Regex-heuristic scan of AGENTS.md / CLAUDE.md prose for concrete rule
hints (don't-edit / generated / run-before-commit / secrets / ci-green
patterns). Emits suggestions in the same format as `adopt`.

### `reconc doctor [repo] [--deep] [--json] [--output PATH]`
Default mode inspects discovery state only. `--deep` adds eight
diagnostic checks: hook-runtime compatibility, native Grok hook trust/loading,
Grok leader steering protocol/extension compatibility, lockfile freshness, audit-log size,
preset/template reference resolution, session-claim age, and static rule
conflicts. Deep mode exits 1 when any check is `FAIL`, 0 when all rows are
`OK` or `WARN`.

### `reconc verify [repo] [--json]`
End-to-end installation health check: PATH, `$RECONC_HOME`, presets, repo
discovery, read-only policy parsing, lockfile freshness, git
pre-commit hook, and agent-hook runtime compatibility. Always exits 0;
WARN rows flag optional misses.

### `reconc status [repo] [--json] [--output PATH]`
One-line, read-only policy health summary. Missing, stale, malformed,
schema-drifted, migration-drifted, and non-portable current lockfiles surface as issues
with explicit `reconc refresh .` remediation. Useful as a session-start ping.

### `reconc done [repo] [--window N] [--require-clean-git] [--json]`
Evidence-complete task-finish gate. It binds current policy, the exact
HEAD/index/worktree candidate when Git is available, active-session evidence,
saved report integrity, current staged command proofs, and typed TASK completion
into a versioned, digested report. An unresolved explicit block for the same
candidate remains blocking until a later explicit non-blocking `check` or `ci`
decision clears it. Text mode prints every failed check and exactly one next
action; JSON emits the full completion report. Exit 0 = done, 2 = blocked,
1 = runtime/input error. `--require-clean-git` adds a clean-tree check.
`--window` is accepted for compatibility but elapsed time never proves
completion.

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
read paths, commands, and claims from the active agent session. In `--staged`
mode, successful-command rules accept only current `reconc exec --staged`
proofs bound to the exact HEAD and index; mutable active-session command
outcomes are not commit evidence. The CLI and its help reject explicit
`--command-success` and `--command-failure` flags with `--staged`; they remain
available for `--base`/`--head` CI ranges.
Missing or stale lockfiles fail closed without writing and require
`reconc refresh .`.

### `reconc exec [repo] [--staged] [--shell] -- COMMAND [ARG ...]`
Execute a command from the repository root and record its real exit status in
the active Reconc session when one exists. `--staged` additionally requires no
tracked-unstaged or untracked paths, verifies that the command leaves HEAD,
the index, and the working tree unchanged, then atomically publishes a bounded
SHA-256 receipt outside the repository. `--shell` accepts one literal command
for platform-shell syntax; direct argv execution is the default. Failed
commands propagate their child exit code and never publish a proof.

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
With explicit evidence flags, runs the same focused evaluation as `fix --next`.
With only a repository path, loads the latest persisted blocking decision and
replays its top remediation when the repository/policy/session candidate is
still current. If it is stale, Reconc reconstructs the exact original
`reconc check` command including success/failure evidence flags instead of
claiming that no remediation is needed.

### `reconc why <rule-id> [repo] [--json] [--terse]`
Prints the full rule from the lockfile (kind, mode, message, paths,
provenance, DEPRECATED label if set). `--terse` emits only kind, mode,
first path, and a shortened message.

---

## Packs & wiring

### `reconc preset list [--json] [--output PATH]` / `reconc preset show <name> [--json] [--output PATH]`
Built-in (`default`, `agent`, `docs-sync`, `release`, `strict`,
`go-assurance`, `bun-assurance`, `python-assurance`, `rust-assurance`,
`shell-assurance`, `cpp-assurance`, `java-assurance`, `php-assurance`,
`csharp-assurance`, `nextjs-assurance`, `svelte-assurance`, `zig-assurance`,
`elixir-assurance`, `powershell-assurance`) + user
presets from `$RECONC_HOME/presets/*.yml`. User-authored presets override bundled
ones on name collision. JSON listing includes each validated manifest and its
declared capabilities when present.

### `reconc template list [--json]` / `reconc template show <name> [--json]`
Rule shape templates (`tests-follow-source`, `docs-follow-code`,
`no-generated-writes`, `ci-green-before-merge`, `authority-change-approval`,
`custom-gate-on-change`, `local-secret-state-read-only`, `verified-change`).
User overrides in `$RECONC_HOME/templates/*.yml`.

### `reconc hook generate <git-pre-commit|claude-code|codex|cursor|opencode|devin-cli|antigravity|kilo|grok> [--json] [--output PATH]`
Emit the hook artefact content without writing to disk.

### `reconc hook install <git-pre-commit|claude-code|codex|cursor|opencode|devin-cli|antigravity|kilo|grok> [repo] [--force] [--json] [--output PATH]`
Write the hook into the repo. Git pre-commit uses Git's active hooks path
(`core.hooksPath`, otherwise `.git/hooks`), updates a Reconc-owned hook
idempotently, preserves inactive legacy hooks, requires `--force` for a foreign
active hook, and always refuses shared external targets. Claude Code and
Codex JSON configs are merged non-destructively;
Cursor writes `.cursor/hooks.json`; OpenCode writes
`.opencode/plugins/reconc.js`; Devin merges `.devin/hooks.v1.json`;
Antigravity merges the top-level
`reconc` hook definition into `.agents/hooks.json`, preserving
non-reconc hook groups; and Kilo Code owns
`.kilo/plugin/reconc.js`. Grok Build owns the dedicated
`.grok/hooks/reconc.json` file and preserves every other project hook file.
Every wrapper-dependent platform installs or verifies the exact executable
repo-local wrapper in the same operation. Codex installation also manages its
`[features].hooks` activation: an explicit user-owned `false` requires
`--force`, and forced activation records the exact original line so uninstall
can restore it. Partial wrapper/target/activation outcomes are reported
explicitly with one recovery command. Managed plugin/files refuse unrelated existing
content unless `--force` is passed.
All non-Git targets are resolved through operating-system filesystem identity
and must stay inside the selected repository. Unix symlinks, Windows reparse
points and 8.3 aliases are handled before containment. Forced malformed-config
backups are private, content-addressed, create-only, and durably synced before
publication.

### `reconc hook uninstall <git-pre-commit|claude-code|codex|cursor|opencode|devin-cli|antigravity|kilo|grok> [repo] [--json] [--output PATH]`
Remove only generator-exact dedicated artifacts or canonical Reconc-owned JSON
entries while preserving unrelated hooks and configuration. Modified or
ambiguous Reconc-looking entries fail closed. Codex removes only its managed
activation block and restores a force-replaced explicit `hooks = false` line
byte-for-byte. The shared repo-local wrapper is deliberately preserved because
another platform may still depend on it.

### `reconc hook status [repo] [--json]`
Validate registered artifacts and activation requirements. States are
`absent`, `installed`, `configured`, `degraded`, `shadowed`, and `unsupported`.
The command checks malformed, incomplete, non-executable, or drifted managed
artifacts, the repo-local wrapper, Codex's enable flag, Git `core.hooksPath`,
Kilo Code pure mode, legacy Kilo Code plugin placement, and Grok's native
project-hook artifact. Static Grok status cannot prove folder trust; `doctor
--deep` additionally runs `grok inspect --json` when the artifact exists.
Each platform reports separate `generated`, `installed`, `executable`,
`configured`, and `live` booleans plus one exact remediation whenever static
configuration is incomplete. It also reports registry-derived `expected_events`, rate-limited
`live_events`, `unseen_events`, `last_seen`, and `last_event` runtime evidence
separately from static activation state. `configured` proves only that the host
can discover a complete static artifact. Codex accepts
`hooks = true` under `[features]`, rejects root-level `hooks=true`, and has no
`SessionEnd` or separate failed-tool route; failed Bash outcomes are inferred
from `PostToolUse`. OpenCode and Kilo Code preserve complete post-tool output,
deduplicate terminal tool errors from `message.part.updated`, and route user
prompts plus pre/post-compaction lifecycle. Their continuation is inferred from
`session.idle`, not a synchronous native Stop gate. Reconc emits native Grok
`Stop` block JSON directly in the normal TUI without a leader, but treats it as
synchronously enforced only when the installed Grok hook guide advertises
blocking Stop decision control. Otherwise `reconc grok` or optional leader
steering over the Unix socket or Windows named pipe provides strict
continuation. Deep doctor reports native Stop capability separately from route
loading; its optional leader probe requires protocol
version 1 and a recognized `_x.ai/interject` response, not just a successful
register handshake. It also requires project-owned inspect metadata and exact
route command tokens; prefix collisions do not satisfy route coverage.
Default text reports seen/expected counts and the last event without listing
every unseen route; the full unseen-event enumeration remains in `--json`.

### `reconc hook sync-scaffold <repo-root-scaffold> [--json]`
Regenerate source-controlled hook artifacts inside a template
`repo-root-scaffold`: `.githooks/pre-commit`, `.codex/hooks.json`,
`.cursor/hooks.json`, `.agents/hooks.json`, `.claude/settings.json`,
`.opencode/plugins/reconc.js`, `.devin/hooks.v1.json`,
`.kilo/plugin/reconc.js`, and `.grok/hooks/reconc.json`. This keeps scaffolded repos on the
same generator truth as `reconc hook install`; do not copy these files
from a source-specific harness. Reconc preflights containment for every target
before the first write, preventing both parent-symlink escapes and partial
scaffold updates.

### `reconc hook claim <repo> <claim-name> [--json] [--output PATH]`
Assert a workflow claim (e.g. `ci-green`). Written to the session
state consulted by later hook-runtime checks and `ci` calls.

### `reconc hook runtime <event> <repo>`
Registry-owned agent-platform event dispatcher. Called from Claude Code,
Codex, Cursor, OpenCode, Devin CLI, Antigravity CLI, Kilo Code, and Grok Build
hook configs, not by users directly. Codex uses only released routes and
infers failed Bash outcomes from `PostToolUse`; OpenCode and Kilo preserve
complete post-tool output, deduplicate terminal tool errors, and route prompt,
compaction, and session lifecycle.

### `reconc grok [repo] [--model ID] [--grok-binary PATH] [--max-continuations N] --prompt TEXT`
Starts the unmodified official `grok agent stdio` ACP runtime in the target
repository. Preflight requires the generated `.grok/hooks/reconc.json`, the
repo-local wrapper, project trust, and a live `grok inspect --json` report that
contains the generator-exact managed artifact, generator-exact executable
wrapper, project-owned source metadata, and every exact native route. The
driver streams Grok's answer and re-prompts the same ACP session while Reconc's strict Stop evaluation returns a
continuation reason. Ctrl-C terminates immediately. The default continuation
limit is 32. ACP uses Grok's `--always-approve` transport because it has no TUI
permission modal; Reconc PreToolUse and Grok's explicit deny rules still run.

---

## Workflow maintenance

### `reconc changelog rotate [repo] [--force] [--lines N] [--json]` / `reconc changelog list-archives [repo] [--json]`
Rotate `docs/changelog.md` when it exceeds the line threshold (default
200). Moves older `##`-sections into
`docs/changelog/archive/YYYY-QN.md`. Rotation is cross-process locked,
crash-idempotent, duplicate-safe, and preserves unrelated archive content when
multiple writers race.

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

### `reconc run on [repo] [--force] [--json]` / `reconc run off [repo] [--json]`
AI-operated switch scoped to one repository, not the whole machine. It routes
continuation through all eight registered agent runtimes. Claude Code, Codex,
Cursor, Devin CLI, and Antigravity CLI expose synchronous Stop
gates; OpenCode and Kilo Code use inferred `session.idle` adapters whose host
boundary is best-effort and fail-open. Reconc emits exact Grok Stop block JSON
without a leader; synchronous stock-TUI enforcement and its continuation bound
are accepted only when the installed Grok guide explicitly advertises the
contract. `reconc grok` remains the explicit ACP path, while passive Stop
sessions can be steered through `_x.ai/interject` over the Unix socket or
Windows named pipe. Eligible leader Stops use strict continuation before policy
evaluation.
Only successfully delivered interjections consume the 32-attempt cap;
transport or protocol failures do not. The cap resets on material progress, a
changed block, or a clean Stop. Before enabling, `run on` validates live policy
sources, the compiled lockfile, and an executable typed TASK disposition. It
fails without mutating state and gives one exact remediation; `--force` is the
explicit exceptional override. Typed `continue` and `claim` states
continue: `Current: none` or an empty Active section still claims queued
executable work. Complete or absent state disables the switch after terminal
gates; blocked state reaches terminal Stop without silently disabling it, and
invalid state fails closed. An explicit interrupt or six repeated no-progress
continuations in the same session releases only that invocation; concurrent
sessions have independent counters and progress fingerprints. Strict Grok
Stops do not consume this six-event guard: their applicable safety bound is 32
successfully delivered leader interjections. Ordinary prompts, session
end, runtime changes, and application restarts never mutate the durable switch.
`off` is the only normal manual disable action. Both commands are idempotent
and log only actual switch transitions. The agent executes these
commands itself; it must not ask the user to operate Reconc.

### `reconc run reset [repo] [--json]`
Recovery-only replacement of `state.bin` with an identity-bound clean disabled
state. Use the exact command printed after corrupt, unsupported, or foreign-root
state errors. It preserves `decisions.jsonl`, archives, and every unrelated run
artifact; it never enables autonomy.

### `reconc run status [repo] [--verbose | --json]`
One-line or JSON snapshot of run mode plus typed TASK disposition:
`enabled`, `task_disposition`, current TASK/Sub-Task, open count, no-progress
state, blocker, and reason. Invalid TASK state is reported as disposition
`invalid`; Stop then fails closed with the validation error. The default line
and JSON schema remain stable. `--verbose` adds complete TASK, blocker, and
latest bounded-decision context.

### `reconc run log [repo] [-n N] [--branch B] [--session S] [--follow] [--json]`
Render the bounded run decision ring: every continuation, material state
transition, policy block, no-progress release, explicit switch, and stop
reason. Continuation records contain bounded identifiers, branch, and counter
metadata, never prompt bodies. Disabled no-op events are not logged.
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
- `task new [repo] --title TEXT [--id ID] [--json]`: atomically add the next or requested collision-free queued row and grammar-correct detail
- `task claim <ID> [repo] [--json]`: activate one executable queued TASK
- `task block [repo] --reason TEXT [--next ID | --no-next] [--json]`: block current; auto-claim the next executable TASK by default or explicitly leave none active
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
report, lock, staged command-proof, and product-wide project-root state; audit
and run-decision JSONL rings; generated workflow-audit binaries; abandoned
repo-local atomic/build temps; and owned `reconc-proof-*` temp trees.
`--dry-run` reports file candidates without deleting them. Owned proof temp
trees use a two-hour inactivity grace. The
global project-state contract keeps at most 256 recognized roots / 128 MiB /
30 days while preserving the current project, live sessions, unknown
directories, and recently active lifecycle roots.
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
Runs the same evidence-complete contract as `reconc done`. It retains exit 1
for a failed gate so existing hook loops keep their error contract. Governed
worktree content remains untouched; the command may update or clear the private
unresolved policy-block receipt under `RECONC_HOME`. `--window` is
compatibility-only and never clears a block.

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
session id, the exact completion decision and blockers, conflicts, and the next
action. `--json` emits the same
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
