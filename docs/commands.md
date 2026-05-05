# reconc -- Command Reference

Full reference for all 37 subcommands. See `reconc <subcommand> --help` for
the exact flag details emitted by the installed binary.

## Daily path

Use this first:

```bash
reconc setup .
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

- `RECONC_HOME` (default `~/.reconc`) -- user config, presets, templates
- `RECONC_AUDIT=1` -- enable the opt-in append-only audit log
- `RECONC_SCHEMA_BASE_URL` -- enterprise override for schema URLs

---

## Setup & inspection

### `reconc init [repo] [--preset NAME] [--force] [--output PATH]`
Scaffolds `.reconc.yml` + a stub `AGENTS.md` for a fresh repo. Multiple
`--preset` flags compose. Refuses to overwrite existing files unless
`--force` is set. `--output` mirrors the primary text or JSON output
to a file while still printing to stdout.

### `reconc bootstrap [repo] [--preset NAME] [--force] [--skip-git-hook] [--skip-agent-hooks] [--json]`
One-shot onboarding: init + compile + install git pre-commit + (if
`.claude/` or `.codex/` are present) install agent hooks.

### `reconc setup [repo] [--preset NAME] [--force] [--skip-git-hook] [--skip-agent-hooks] [--json]`
Friendly alias for `bootstrap`. Prefer this in human-facing guides when
you want the shortest mental model.

### `reconc adopt [repo] [--yaml | --json | --apply]`
Detects common tooling (JS/TS, Python, Rust, Go, CI, generated dirs)
and emits matching-rule suggestions. `--apply` appends them to
`.reconc.yml` idempotently.

### `reconc extract [repo] [--from PATH] [--yaml | --json]`
Regex-heuristic scan of AGENTS.md / CLAUDE.md prose for concrete rule
hints (don't-edit / generated / run-before-commit / secrets / ci-green
patterns). Emits suggestions in the same format as `adopt`.

### `reconc doctor [repo] [--deep] [--json] [--output PATH]`
Default mode inspects discovery state only. `--deep` adds six
diagnostic checks: hook-runtime compatibility, lockfile freshness,
audit-log size, preset/template reference resolution, session-claim
age, and static rule conflicts. Deep mode exits 1 when any check is
`FAIL`, 0 when all rows are `OK` or `WARN`.

### `reconc verify [repo] [--json]`
End-to-end setup health check: PATH, `$RECONC_HOME`, presets, repo
discovery, read-only policy parsing, lockfile freshness, git
pre-commit hook, and agent-hook runtime compatibility. Always exits 0;
WARN rows flag optional misses.

### `reconc status [repo] [--json] [--output PATH]`
One-line policy health summary. Useful as a session-start ping.

### `reconc done [repo] [--window N] [--require-clean-git] [--json]`
Terse task-finish gate. Prints `done` when the lockfile is present,
fresh enough for the known audit window, and no recent blocking audit
entry exists. Prints `blocked: <next action>` and exits 2 when the task
is not ready. `--require-clean-git` also requires a clean working tree.

---

## Compile & evaluate

### `reconc compile [repo] [--json] [--strict-conflicts] [--output PATH]`
Produces `.reconc/policy.lock.json` from sources. With
`--strict-conflicts`, exits 1 when any rule conflict is detected.

### `reconc check [repo] [--read PATH] [--write PATH] [--command CMD] [--command-success CMD] [--command-failure CMD] [--claim NAME] [--auto-claim] [--json] [--terse] [--output PATH]`
The core policy evaluator. Exit 0 = pass/warn, 2 = block, 1 = error.
`--terse` emits ~50-token JSON optimised for hook-loop calls.
`--auto-claim` detects CI environment and auto-asserts `ci-green`.

### `reconc ci [repo] (--staged | --base REF [--head REF]) [--read PATH] [--command CMD] [--claim NAME] [--auto-claim] [--json] [--output PATH]`
Git-aware check. Derives write paths from the working-tree index or a
`base..head` range instead of explicit `--write` flags.

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
Built-in (`default`, `agent`, `docs-sync`, `release`, `strict`) + user presets from
`$RECONC_HOME/presets/*.yml`. User-authored presets override bundled
ones on name collision.

### `reconc template list [--json]` / `reconc template show <name> [--json]`
Rule shape templates (`tests-follow-source`, `docs-follow-code`,
`no-generated-writes`, `ci-green-before-merge`). User overrides in
`$RECONC_HOME/templates/*.yml`.

### `reconc hook generate <git-pre-commit|claude-code|codex> [--json] [--output PATH]`
Emit the hook artefact content without writing to disk.

### `reconc hook install <git-pre-commit|claude-code|codex> [repo] [--force] [--json] [--output PATH]`
Write the hook into the repo. Git pre-commit is a fresh file; Claude
Code / Codex JSON configs are merged non-destructively (idempotent:
reconc-owned entries are identified by the `reconc hook runtime`
command prefix and replaced wholesale on re-install).

### `reconc hook claim <repo> <claim-name> [--json] [--output PATH]`
Assert a workflow claim (e.g. `ci-green`). Written to the session
state consulted by later `check`/`ci` calls.

### `reconc hook runtime <event> <repo>`
Agent-platform event dispatcher. Called from Claude Code / Codex hook
configs, not by users directly.

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
Aggregate summary: totals, by-decision, by-event, top rules.

### `reconc audit export [repo]`
Raw JSONL dump on stdout for external tooling.

### `reconc session-briefing [repo] [--json]`
Compact (~400 token) session-start state dump: lockfile state, recent
audit activity, top firing rule, next action. Read-only; it never
repairs or rewrites the lockfile.

### `reconc context size [repo] [--limit N] [--files PATH,PATH,...] [--json]`
Guards the auto-loaded session-file token budget (default 20000
tokens). Lists per-file size + approximate tokens. Exit 1 over limit
so CI gates can block budget-growing PRs.

### `reconc start [repo] [--write PATH] [--force] [--json] [--minimal]`
Renders a canonical `start.md` onboarding / reentry doc from the
current state. Reuses session-briefing + audit-tail data. `--minimal`
emits a compact 3-line summary.

### `reconc post-task-check [repo] [--window N] [--require-clean-git] [--json]`
Pre-done gate: fresh lockfile + no blocking audit entries in the last
N minutes (default 10). Exit 1 on any check failure.

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
snapshot as structured data.

### `reconc completion <bash|zsh|fish>`
Emit a shell completion script. Install one-liners:

```bash
reconc completion bash > /usr/local/etc/bash_completion.d/reconc
reconc completion zsh  > /usr/local/share/zsh/site-functions/_reconc
reconc completion fish > ~/.config/fish/completions/reconc.fish
```

### `reconc version [--json]`
Print the build version as text or JSON. Equivalent to top-level
`reconc --version`.
