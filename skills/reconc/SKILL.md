---
name: reconc
description: Operate Reconc policy and evidence in a repository that uses it, or when the user requests Reconc setup, diagnosis, or completion verification. Provides the CLI workflow and precise hook, CI, and MCP boundaries.
---

# reconc

## Core workflow

`reconc` compiles repository policy into a deterministic contract and checks
real agent evidence against it. It does not invent acceptance criteria,
priorities, approvals, or test results.

1. **Inspect** the versioned machine briefing.
2. **Select** the exact next action and its typed `argv`, `cwd`, authorization,
   and required evidence; never reconstruct an action from display text.
3. **Gather** only truthful reads, writes, command outcomes, and claims.
4. **Handle** a block with the emitted remediation before writing or retrying.
5. **Prove** the completed candidate with the final gate and report the evidence.

For authorized repository changes, use this loop:

```bash
reconc session-briefing . --json
reconc check . --write path/to/changed-file
reconc next .
reconc done .
```

On a block, stop the write, read `violations[].recommended_action`, and run
`reconc next .`. Use `reconc exec . --staged -- <command>` and
`reconc ci . --staged` for commit-bound command proof. Run
`reconc proof . --format markdown` when a portable reviewer record is needed.
Exit `0` is pass or warn, `1` is a runtime/input error, and `2` is a block.

## Trigger and contract

For a read-only review, use the briefing and needed inspection commands only.
Missing or stale policy is a finding, not permission to initialize or refresh.
Follow existing user authorization for mutations; a suggested action does not
grant it. Do not bootstrap the Reconc product source repository itself.

The CLI is sufficient without this skill or an MCP connection. The skill
teaches the workflow; native hooks enforce supported live boundaries; CI checks
the selected Git candidate. The optional `reconc mcp gateway` gates explicitly
routed downstream tools, not every action of the host. Read the platform
reference before choosing or configuring that integration.

Never fake evidence, bypass a block, or claim native enforcement
without live `reconc hook status . --json` proof. When autonomous TASK work is
requested, operate the repository run switch yourself; do not ask the user to
type Reconc commands.

## Lazy references

Fetch only the detail required for the current decision:

```bash
reconc agent-intro --list-sections
reconc agent-intro --section <section-id>
```

The embedded guide exposes stable IDs for bootstrap, repository upgrade,
decision loops, rule inspection, claims, platform integration, autonomous run
control, output modes, locations, and golden rules. The skill's owned detail
references are:

- [install and bootstrap](references/install-and-bootstrap.md)
- [workflow and evidence](references/workflow-and-evidence.md)
- [platform integration](references/platform-integration.md)
- [completion and boundaries](references/completion-and-boundaries.md)

The registry currently covers Claude Code, Codex, GitHub Copilot, Cursor,
OpenCode, Devin CLI, Antigravity CLI, Kilo Code, Oh My Pi, Pi Coding Agent,
ZCode, Grok Build, and Kimi Code CLI. Host-specific proof includes
`surface_events`, `workspaceOpen`, `AskQuestion`, `afterShellExecution`,
`postToolUseFailure`, `output.metadata.exit`, and `reconc why mcp`; read the
platform reference before making a stronger claim.

## Boundaries

Inspection and evaluation do not refresh policy implicitly. Refresh, init,
bootstrap apply/remove, repository sync, hook installation, TASK mutation,
run-state mutation, and prune are explicit state changes. Git pre-commit and
the CLI remain the deterministic backstop when a host cannot enforce natively.
