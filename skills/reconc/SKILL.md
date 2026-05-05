---
name: reconc
description: Use when Codex, OpenCode, Claude Code, or another coding agent should bootstrap, maintain, or obey reconc repository policy. Covers the Repository Control Compiler, install/build choice, minimal daily loop, truthful evidence collection, policy checks, remediation, task-finish gates, CI/release use, and platform-specific enforcement limits without adding heavy process or option sprawl.
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

Keep the workflow simple. `reconc` is a guardrail, not a project manager, task
tracker, or substitute for tests and human review.

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

1. Check policy health.
2. Do the actual work.
3. Report only evidence that really happened.
4. Ask `reconc` for the next remediation when blocked.
5. Run the final done gate before claiming completion.

Never fake reads, commands, claims, or write paths to satisfy policy. If an
agent runtime cannot enforce a rule natively, use the CLI loop and git
pre-commit as the backstop.

## Install Or Build

Prefer an installed `reconc` binary:

```bash
reconc --help
```

If the binary is not installed and the current repo is the `reconc` source
tree, build a temporary session binary:

```bash
go build -o /tmp/reconc ./cmd/reconc
```

Then use `/tmp/reconc` in commands for this session. In any other repo, do not
invent an install path; tell the user `reconc` is missing and ask whether to
install or build it.

## Bootstrap A Repo

For a new target repo:

```bash
reconc setup .
reconc status .
```

`setup` is the human-facing onboarding path. It scaffolds `.reconc.yml` and
`AGENTS.md` when missing, compiles the lockfile, installs git hooks, and wires
native agent hooks when supported directories such as `.claude/` or `.codex/`
already exist.

For a lighter/manual start:

```bash
reconc init .
reconc compile .
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
reconc status .
```

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

Treat `done` as the minimal task-finish gate:

- `done`: task may be closed
- `blocked: ...`: do the next action first
- exit code `2`: blocking policy remains

For staged git work, prefer:

```bash
reconc ci . --staged \
  --read docs/documentation.md \
  --command-success 'go test ./...'
```

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
reconc status .              # one-line health
reconc check . ...           # evaluate current evidence
reconc next .                # next remediation
reconc done .                # final task gate
reconc verify .              # setup health, read-only
reconc doctor . --deep       # deeper diagnostics
reconc ci . --base HEAD~1 --head HEAD
reconc preset list
reconc preset show agent
reconc agent-intro           # built-in guide for humans and agents
```

`status`, `verify`, and `session-briefing` are diagnostic/read-only. `compile`,
`setup`, `init`, hook installation, `adopt --apply`, and audit logging can write.

## Platform Model

Know what is hard-enforced versus self-enforced:

| Capability | Claude Code | Codex | OpenCode | Other agents |
|---|---|---|---|---|
| Native hook install | `.claude/settings.json` | `.codex/hooks.json` | none currently | none by default |
| Pre-write file blocking | Hard for Edit/Write/MultiEdit | Soft self-check | Soft self-check | Soft self-check |
| Read/write evidence capture | Hard via hooks | Partial/manual | Manual CLI evidence | Manual CLI evidence |
| Bash command interception | Hard | Hard for Bash hooks | Manual CLI evidence | Manual CLI evidence |
| Stop/final gate | Hard | Hard where hooks run | `reconc done` | `reconc done` |
| Commit backstop | Git pre-commit | Git pre-commit | Git pre-commit | Git pre-commit |

Claude Code has the strongest native integration. Codex has native hook wiring
for session, Bash, and stop events, but agents must still run explicit file
checks for file-level evidence. OpenCode and other agents should use the same
CLI evidence loop and rely on git pre-commit as the hard repository backstop.

Never claim stronger enforcement than the platform can provide.

## When Policy Is Stale

If `status`, `verify`, or `check` reports a stale or missing lockfile:

```bash
reconc compile .
reconc status .
```

Do not hand-edit `.reconc/policy.lock.json`. The lockfile is generated output.

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

- prefer five daily commands over many knobs
- prefer warning presets for agent guidance until a team proves it wants blocks
- keep policy repo-local and explicit
- compile deterministic lockfiles
- do not use `reconc` to replace tests, review, or user approval
