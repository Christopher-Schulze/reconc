---
name: reconc
description: Use when a coding agent should bootstrap, maintain, or obey reconc repository policy. Covers the Repository Control Compiler, install/build choice, minimal daily loop, truthful evidence collection, policy checks, remediation, task-finish gates, CI/release use, and registry-backed platform enforcement limits without adding heavy process or option sprawl.
---

# reconc

## Purpose

`reconc` is the Repository Control Compiler. It turns repo-local policy from
`AGENTS.md`, `.reconc.yml`, presets, templates, and policy YAML into
`.reconc/policy.lock.json`, then checks actual agent evidence against that
compiled contract.

Use it to add a small amount of determinism to AI coding sessions:

- stop writes to generated or protected paths
- require reads before sensitive edits
- require tests, commands, claims, or matching docs changes
- expose one next remediation instead of a giant rule lecture
- gate task completion with a terse `done` / `blocked` result

Keep the workflow simple. `reconc` may enforce a repository's existing typed
TASK control plane, but it never invents product priorities, acceptance, test
evidence, or human approval.

## Trigger

Use this skill in Codex, OpenCode, Claude Code, or any other agent runtime
when:

- the repo has `.reconc.yml`, `.reconc/policy.lock.json`, or `AGENTS.md`
  policy blocks
- the user asks to bootstrap repository controls, agent guardrails, policy
  lockfiles, task-finish gates, or deterministic agent behavior
- an agent is about to make code, docs, or config changes in a repo that should
  be checked before completion
- the user asks whether a task is safe, done, blocked, or missing evidence

Do not use it for one-off shell questions or when the user explicitly says not
to touch policy, hooks, lockfiles, or repo controls.

## Agent Contract

This skill is platform-agnostic. The loop is identical across agents:

1. Read the versioned machine briefing.
2. Do the actual work.
3. Report only evidence that really happened.
4. Ask `reconc` for the next remediation when blocked.
5. Run the final done gate before claiming completion.

When the user requests autonomous TASK execution, the agent also owns the run
switch. Never ask the user to type Reconc commands.

Never fake reads, commands, claims, or write paths to satisfy policy. If an
agent runtime cannot enforce a rule natively, use the CLI loop and git
pre-commit as the backstop.

Reconc is not an operating-system sandbox. If the agent is treated as a
hostile same-user process, put the repository inside an external sandbox and
enforce final truth in protected remote CI or branch rules.

## Install Or Build

Prefer an installed `reconc` binary:

```bash
reconc --help
```

If the binary is not installed and the current repo is the `reconc` source
tree, build an owned, pruneable session binary:

```bash
mkdir -p .reconc/cache
go build -o .reconc/cache/reconc-session ./cmd/reconc
.reconc/cache/reconc-session install-cli
reconc --version
```

The path-qualified binary is needed only for that one installation call.
`install-cli` atomically publishes the exact running build and proves bare
`reconc` resolves to it. If PATH activation needs a new terminal, apply the
exact emitted remediation before bootstrap. In any other repo, use the
portable binary shipped with its Reconc toolkit for the same one-time command;
never keep navigating versioned artifact paths.

## Bootstrap A Repo

For a new target repo:

```bash
reconc init .
reconc session-briefing . --json
```

`init` is the canonical CLI onboarding path. It scaffolds `.reconc.yml` and
`AGENTS.md` when missing, compiles the lockfile, installs git hooks, and wires
native agent hooks when supported directories such as `.claude/`, `.codex/`,
`.cursor/`, `.opencode/`, `.devin/`, `.agents/`, `.kilo/`, or `.grok/`
already exist.
Init mutation performs the same exact running-build installation, fails
before repository writes when bare `reconc` still does not resolve to it, and
transactional verification repeats that check.

For the full repo-local governance rollout with copied Reconc toolkit, harness,
root scaffold, `start.md`, TASK files, and repo-local release binaries, have an
agent follow `harness/template/BOOTSTRAP.md` from the copied toolkit instead of
assuming canonical init copies a complete toolkit.

For a lighter/manual start:

```bash
reconc init .
reconc refresh .
```

Default new repos should normally use the bundled `default` + `agent` presets.
Only add stronger presets when the repo is ready for them:

- `docs-sync`: public surface changes should update user-facing docs
- `strict`: source edits require tests, architecture reads, and `ci-green`
- `release`: release manifests/artifacts require changelog, checksums, and
  verification

## Daily Agent Loop

Run this compact loop around actual work:

```bash
reconc session-briefing . --json
```

This one read-only response carries `format_version`, TASK/Sub-Task, policy
delta, exact remediation, and repository-run state. Fetch static detail only
when needed with `reconc agent-intro --section NAME`.

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

The current v0.9.1 release can export that same candidate as portable reviewer evidence
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
Code, Codex, GitHub Copilot, Cursor, Devin CLI, and Antigravity CLI expose
synchronous Stop continuation. OpenCode and Kilo Code use inferred
`session.idle`, so their host continuation remains best-effort and fail-open.
Grok Build has hard native PreToolUse. Reconc also emits exact native Stop
blocks without a leader, but accepts synchronous enforcement only when the
installed Grok hook guide advertises blocking Stop decision control. Passive
Stop distributions may use optional leader steering over the Unix socket or
Windows named pipe. Only delivered
interjections consume the 32-attempt no-progress series; capability-proven
native hosts suppress duplicate interjection.
`RECONC_GROK_STEER=0` disables only leader steering. Managed activation
requires exact hook/wrapper artifacts and route tokens. Deep doctor reports
native Stop capability and separately probes protocol 1 plus `_x.ai/interject`.
Typed `continue` and `claim` states continue; an empty active slot claims queued
executable work. Complete or absent state disables the switch after terminal
gates, blocked state reaches terminal Stop without silently disabling it, and
invalid state fails closed. An interrupt or six repeated no-progress
continuations releases only the current invocation. Prompt text, session
boundaries, runtime changes, and application restarts never mutate the durable
switch; `run off` is the only manual disable action. Pre-write, TASK mutation,
pre-commit, and terminal Stop gates remain authoritative.

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

## Platform Model

The typed registry owns native event coverage, fallback routes, failure and
timeout policy, output budgets, artifact paths, and activation probes:

| Platform | Artifact | Integration model |
|---|---|---|
| Claude Code | `.claude/settings.json` | Native session, tool, permission, Stop, cleanup, and compact-session recovery hooks |
| Codex | `.codex/hooks.json` | Native session, tool, permission, evidence, and Stop hooks; no `SessionEnd` |
| GitHub Copilot | `.github/hooks/reconc.json` | Repository hooks for Copilot CLI and coding agent; host timeouts remain fail-open |
| Cursor | `.cursor/hooks.json` | Registry-driven Agent/Cmd+K, Tab, CLI, and eligible cloud routes; `surface_events`, workspace liveness, decisions, outcomes, and guarantees are event-specific |
| OpenCode | `.opencode/plugins/reconc.js` | Thin project plugin with strict shell exits and inferred bounded async idle continuation; decisions and state stay in Go |
| Devin CLI | `.devin/hooks.v1.json` | Native lifecycle plus post-compaction recovery |
| Antigravity CLI | `.agents/hooks.json` | Invocation, tool, evidence, and Stop adapters |
| Kilo Code | `.kilo/plugin/reconc.js` | Thin CLI/VS Code project plugin with strict shell exits and inferred bounded async idle continuation; disabled when `KILO_PURE` is set |
| Grok Build | `.grok/hooks/reconc.json` | Native lifecycle and hard PreToolUse; project trust required; capability-probed native Stop or optional local leader fallback |

Run `reconc hook status . --json` before making enforcement claims.
`configured` proves a complete static artifact; `discoverable` means the named
host surface scans its path; `loaded` requires a current session/init route;
`observed` requires that exact route; `enforced` requires a disposable negative
probe that stopped the side effect; `inferred` is weaker host lifecycle;
`degraded` is missing or unproven required behavior; `unsupported` means no
sound host boundary. Never promote one state into another.

Cursor uses one project file, but desktop Agent, Cmd+K, Tab, interactive CLI,
print CLI, and cloud agents do not promise identical event delivery. Use the
same Reconc semantics when the same event fires and keep every unseen route
unproven. `postToolUse` is success, `postToolUseFailure` is failure, and
`afterShellExecution` is liveness only because it has no authoritative exit
status. Cursor CLI uses `agent`; `cursor-agent` is its compatibility alias.
`surface_events` lists the documented routes for each CLI mode.
`workspaceOpen` is sessionless loading evidence only. Cursor currently emits
no generic tool hooks for `AskQuestion`, so never claim Reconc gated that host
action.

OpenCode and Kilo accept shell success only from integer
`output.metadata.exit == 0`. Their `session.idle` continuation calls only
asynchronous `promptAsync`, is generation-deduplicated and capped, and remains
fail-open/inferred. A missing API, rejected request, or invalid response is not
delivered continuation.

MCP repository effects are opt-in exact mappings in `.reconc.yml`. Use
`reconc why mcp .` to inspect the compiled contract. Never treat an unknown
identity, malformed selector value, unknown outcome, or `external` effect as
repository evidence. Cursor can strictly deny unclassified calls through its
dedicated MCP pre-hook. OpenCode/Kilo generic hooks cannot identify
unconfigured MCP calls soundly, so report strict unclassified deny as
unavailable there.

`installed`, `degraded`, `shadowed`, and `unsupported` require the status
detail to be handled or reported. Generic agents use explicit CLI checks.
Every platform keeps Git pre-commit as the hard repository backstop.

## When Policy Is Stale

If `status`, `doctor`, or `check` reports a stale or missing lockfile:

```bash
reconc refresh .
reconc status .
```

Read-only commands never refresh implicitly. Do not hand-edit
`.reconc/policy.lock.json`; it is generated output.

## Agent Behavior

When `reconc` blocks:

1. Read the violation and recommended action.
2. Run `reconc next .` for the shortest remediation.
3. Fix the real missing evidence or source issue.
4. Re-run `reconc check . ...`.
5. Finish with `reconc done .`.

When `reconc` warns:

- report the warning if it matters for the user-visible outcome
- do not inflate the workflow unless the warning points to a real missed step

When no policy exists:

- ask whether to bootstrap if repository controls are relevant
- otherwise proceed normally

## Output Discipline

When reporting to the user, keep it concrete:

- mention the command that passed or blocked
- name blocking rule IDs when available
- separate hard blocks from warnings
- say when a platform limitation means enforcement was self-checked
- never present a warning-only result as a hard failure

## Design Boundary

`reconc` should stay low-friction:

- prefer the canonical daily loop over option sprawl
- prefer warning presets for agent guidance until a team proves it wants blocks
- keep policy repo-local and explicit
- compile deterministic lockfiles
- do not use `reconc` to replace tests, review, or user approval
