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
```

Then use `.reconc/cache/reconc-session` in commands for this session. In any
other repo, do not invent an install path; tell the user `reconc` is missing
and ask whether to install or build it.

## Bootstrap A Repo

For a new target repo:

```bash
reconc bootstrap .
reconc session-briefing . --json
```

`bootstrap` is the minimal CLI onboarding path. It scaffolds `.reconc.yml` and
`AGENTS.md` when missing, compiles the lockfile, installs git hooks, and wires
native agent hooks when supported directories such as `.claude/`, `.codex/`,
`.cursor/`, `.opencode/`, `.devin/`, `.agents/`, `.kilo/`, or `.grok/`
already exist.

For the full repo-local governance rollout with copied Reconc toolkit, harness,
root scaffold, `start.md`, TASK files, and repo-local release binaries, have an
agent follow `harness/template/BOOTSTRAP.md` from the copied toolkit instead of
trying to automate it with the minimal CLI bootstrap.

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

Current source builds after the immutable v0.8.6 release can export that same
candidate as portable reviewer evidence without running missing commands or
persisting a new policy decision:

```bash
reconc proof . --format markdown --output proof.md
```

For autonomous repository execution:

```bash
reconc run on .
reconc run status .
reconc run off .
```

Repository mode is durable for this repository, not machine-global. Claude
Code, Codex, GitHub Copilot, Cursor, Devin CLI, and Antigravity CLI expose
synchronous Stop continuation. OpenCode and Kilo Code use inferred
`session.idle`, so their host continuation remains best-effort and fail-open.
Grok Build has hard native PreToolUse. Reconc also emits exact native Stop
blocks without a leader, but accepts synchronous enforcement only when the
installed Grok hook guide advertises blocking Stop decision control. Passive
Stop distributions use `reconc grok . --prompt "..."` or optional leader
steering over the Unix socket or Windows named pipe. Only delivered
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
reconc verify .              # installation health, read-only
reconc doctor . --deep       # deeper diagnostics
reconc hook status . --json  # exact platform activation truth
reconc run status .          # run mode and typed TASK disposition
reconc ci . --base HEAD~1 --head HEAD
reconc preset list
reconc preset show agent
reconc agent-intro           # built-in guide for humans and agents
```

`status`, `verify`, `doctor`, `check`, `ci`, `assert`, `can`, `why`,
`task status`, `task validate`, `task check-done`,
`run status`, `run log`, `session-briefing`, `start` without `--write`, `post-task-check`, `done`, `proof`, and
`tui` never mutate policy or refresh the lockfile. With `RECONC_AUDIT=1`,
enforcement commands may append decision records. `refresh`, `compile`,
`watch`, `bootstrap`, `init`, hook installation, `hook sync-scaffold`,
`adopt --apply`, `run on`, `run off`, and audit logging can write.

## Platform Model

The typed registry owns native event coverage, fallback routes, failure and
timeout policy, output budgets, artifact paths, and activation probes:

| Platform | Artifact | Integration model |
|---|---|---|
| Claude Code | `.claude/settings.json` | Native session, tool, permission, Stop, cleanup, and compact-session recovery hooks |
| Codex | `.codex/hooks.json` | Native session, tool, permission, evidence, and Stop hooks; no `SessionEnd` |
| GitHub Copilot | `.github/hooks/reconc.json` | Repository hooks for Copilot CLI and coding agent; host timeouts remain fail-open |
| Cursor | `.cursor/hooks.json` | Native session, file, shell, evidence, and Stop adapters |
| OpenCode | `.opencode/plugins/reconc.js` | Thin project plugin; decisions and state stay in Go |
| Devin CLI | `.devin/hooks.v1.json` | Native lifecycle plus post-compaction recovery |
| Antigravity CLI | `.agents/hooks.json` | Invocation, tool, evidence, and Stop adapters |
| Kilo Code | `.kilo/plugin/reconc.js` | Thin project plugin; disabled when `KILO_PURE` is set |
| Grok Build | `.grok/hooks/reconc.json` | Native lifecycle and hard PreToolUse; project trust required; `reconc grok` supplies strict ACP continuation |

Run `reconc hook status . --json` before making enforcement claims. `configured`
means configuration is complete and discoverable, not that a live process has
proven it loaded the file. `installed`, `degraded`, `shadowed`, and
`unsupported` require the detail field to be handled or reported. Generic
agents use explicit CLI checks. Every platform keeps Git pre-commit as the hard
repository backstop.

## When Policy Is Stale

If `status`, `verify`, or `check` reports a stale or missing lockfile:

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
