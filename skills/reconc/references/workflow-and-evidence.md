# Workflow and evidence

## Daily Agent Loop

Run this compact loop around actual work:

```bash
reconc session-briefing . --json
```

This one read-only response carries `format_version`, TASK/Sub-Task, policy
delta, exact remediation, and repository-run state. Fetch static detail only
when needed with `reconc agent-intro --section <section-id>`.

Before or during edits, collect explicit evidence. At the end of a task, check
the real touched surface:

```bash
reconc check . \
  --write path/changed.go \
  --read docs/documentation.md \
  --command-success 'go test ./...'
```

If blocked or unclear:

```bash
reconc next .
```

Before claiming completion:

```bash
reconc done .
```

The running build can export that same candidate as portable reviewer evidence
without running missing commands or persisting a new policy decision:

```bash
reconc proof . --format markdown --output proof.md
```

For autonomous repository execution:

```bash
reconc run on
reconc run status
reconc run off
```

Repository mode is durable for this repository, not machine-global. Claude
Code, Codex, GitHub Copilot, Cursor, Devin CLI, Antigravity CLI, ZCode, and Kimi Code
CLI expose synchronous Stop continuation. Oh My Pi exposes awaited native
main-session Stop continuation capped at eight accepted requests. OpenCode and
Kilo Code use inferred
`session.idle`, so their host continuation remains best-effort and fail-open.
Pi uses inferred fail-open `agent_settled` continuation with at most ten
requests per session; `sendUserMessage` provides no delivery acknowledgement.
Grok Build has hard native PreToolUse. Reconc also emits exact native Stop
blocks without a leader, but accepts synchronous enforcement only when the
installed Grok hook guide advertises blocking Stop decision control. Passive
Stop distributions may use optional leader steering over the Unix socket or
Windows named pipe. Only delivered
interjections consume the 32-attempt no-progress series; capability-proven
native hosts suppress duplicate interjection.
Only a changed material-event snapshot or a clean Stop resets that series;
reason wording alone does not. Spawned Grok children receive exactly one
`RECONC_GROK_STEER=0` entry after inherited duplicates are removed; it disables
only leader steering. Managed activation
requires exact hook/wrapper artifacts and route tokens. Deep doctor reports
native Stop capability and separately probes protocol 1 plus `_x.ai/interject`.
Typed `continue` and `claim` states continue; an empty active slot claims queued
executable work. Complete, absent, blocked, and invalid/non-executable state
reaches terminal Stop after persisting its distinct automatic disable reason;
only an explicit user stop authorizes `run off`. Resolve a blocked TASK and run
`reconc run on .` to resume. An interrupt or six repeated no-progress
continuations releases only the current invocation. Prompt text, session
boundaries, runtime changes, and application restarts never mutate the durable
switch. Pre-write, TASK mutation, pre-commit, and terminal Stop gates remain
authoritative.

If `reconc task status .` finds a configured TASK control plane, also run
`reconc task check-done .` and use `reconc task promote .` only after every
real Sub-Task and configured evidence field is complete. Use `task block`,
`resume`, or `split` for actual state changes; never hand-edit multiple TASK
files into a half-transition.

Treat `done` as the minimal task-finish gate:

- `done`: task may be closed
- `blocked: ...`: do the next action first
- exit code `2`: blocking policy remains

For staged git work, prefer:

```bash
reconc exec . --staged -- go test ./...
reconc ci . --staged \
  --read docs/documentation.md
```

`exec --staged` publishes command success only when the real exit code is zero
and HEAD plus the staged index remain unchanged. Do not substitute mutable
agent-hook outcomes or `ci --command-success` for a staged proof.

## Evidence Rules

Pass only evidence that actually happened:

- `--write`: files you changed or intend to change
- `--read`: files you really read before editing
- `--command-success`: commands that really completed successfully
- `--claim`: claims that are true in this session, such as `ci-green`

Never fake evidence to satisfy policy. If policy asks for a command, run the
command or report why it cannot be run.

When unsure which paths to pass, use the changed files from `git status` or
`git diff --name-only`. Do not pass broad path globs just to make the check
look complete.

## Common Commands

Use the shortest command that answers the current question:

```bash
reconc session-briefing . --json # versioned session/reentry handshake
reconc status .              # one-line health
reconc task status .         # bounded current TASK context
reconc task validate .       # typed control-plane validation
reconc check . ...           # evaluate current evidence
reconc next .                # next remediation
reconc done .                # final task gate
reconc proof . --format markdown # portable reviewer evidence
reconc doctor --global       # global installation and ownership truth
reconc doctor . --deep       # deeper diagnostics
reconc sources . --json      # body-free effective source provenance
reconc audit verify . --json # retained audit chain integrity
reconc hook status . --json  # exact platform activation truth
reconc hook evidence-status . --json # persistent evidence-taint truth
reconc why mcp .             # compiled MCP mappings and unclassified mode
reconc run status .          # run mode and typed TASK disposition
reconc ci . --base HEAD~1 --head HEAD
reconc preset list
reconc preset show agent
reconc agent-intro           # built-in guide for humans and agents
```

Inspection, evaluation, planning, and rendering commands never refresh policy
implicitly. Explicit `--output` flags may publish the requested report or
plan, and `RECONC_AUDIT=1` lets enforcement commands append chained decision
evidence. Policy or control-state mutation is explicit through `refresh`,
`init`, `bootstrap apply|remove`, `repo sync apply|resolve|recover`,
`install-cli`, `update`, `uninstall`, `adopt --apply`, hook installation,
uninstallation, scaffold sync, claim/evidence resolution/runtime routes,
`exec`, TASK mutators, `run on|off|reset`, and `prune`.
