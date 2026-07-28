# reconc - Agent Integration Guide

## What reconc Does

`reconc` (Repository Control Compiler) compiles repo policy from YAML / Markdown into a lockfile, then evaluates your proposed actions (writes, reads, commands, claims) against that lockfile. Its core policy and evidence runtime runs offline, uses no LLM inference, and returns deterministic JSON with a `decision` of `pass`, `warn`, or `block`. The explicit `reconc grok` command delegates authenticated inference to an external Grok ACP process.

**You do not interpret policy. reconc tells you what is allowed and what is not.**

## Exit Codes (Stable Contract)

- `0` = pass or warn (non-blocking)
- `1` = runtime/input error (reconc itself is unhappy)
- `2` = at least one blocking violation (you must fix before proceeding)

Treat exit 2 as "stop writing and remediate first".

## Rule Kinds

| Kind | Meaning | What the Agent Must Do |
|---|---|---|
| `deny_write` | Path is protected | Do NOT write to matched paths |
| `require_read` | Reads required before writing | Read the required file(s), then re-check |
| `require_command` | Command must be attempted | Run the command, assert via `--command` |
| `require_command_success` | Command must succeed | Run it; use `reconc exec --staged` for commit evidence |
| `forbid_command` | Command is banned | Do not run it; use the suggested alternative |
| `couple_change` | Writes must come in pairs | Edit the paired path(s) in the same change |
| `require_claim` | Workflow sign-off required | Assert via `reconc hook claim . <name>` |
| `require_fresh_file` | Artifact must be recent | Regenerate / touch the referenced file |
| `require_evidence` | Text must (not) appear in a file | Update the evidence file to satisfy assertions |
| `require_script` | A script must pass | Run it; fix whatever it flags; re-check |
| `require_assurance` | Native bounded repository gates must pass | Apply the first exact finding, collect real command/proof evidence, then re-check |
| `all_of` / `any_of` / `not` | Composite | Resolve each sub-check; see explanation |

## Bootstrap (New Repo)

Canonical path:
```bash
reconc init .
```
One command: verifies the running user CLI, plans and applies a create-only
profile, compiles policy, installs detected eligible hooks, writes the private
rollback records plus `.reconc/install.lock.json`, and verifies the result.
Use `--profile existing|minimal|governed|advanced` when the repository is not a
fresh unambiguous target. `reconc bootstrap .` is a compatibility alias.

Detect existing conventions and propose matching rules:
```bash
reconc adopt . --yaml       # preview as YAML
reconc adopt . --apply      # append to .reconc.yml (idempotent)
```

Stack-aware policy-pack recommendations are review-only. `adopt --apply` never
adds them to `extends`. Inspect the declared capabilities and select a pack
explicitly only when its inputs, evidence, and gates fit the repository. Go,
Bun, npm, pnpm, Yarn, TypeScript, Python, Rust, Shell, C/C++, Java, PHP, C#,
Next.js, Svelte/SvelteKit, Zig, Elixir, and PowerShell have bundled assurance
packs. Node command suggestions require one evidenced manager and a non-empty
matching `package.json` script. Manager conflicts are review warnings; Reconc
never guesses a manager or invents `test`, `lint`, `build`, or `typecheck`.
The packs never install or execute toolchains; they evaluate native source
checks and successful command evidence supplied by the target repository.

Verify installation health end-to-end:
```bash
reconc verify .
reconc hook status . --json
reconc session-briefing . --json
```

After changing policy sources, refresh the generated contract explicitly:
```bash
reconc refresh .
```
Inspection and enforcement commands never compile or write the lockfile.

## Repository Upgrade

A global CLI update never implies that repository-owned hooks, harness files,
or generated artifacts were updated. Plan first:

```bash
reconc repo sync plan . --output /tmp/reconc-sync.json
reconc repo sync apply --plan /tmp/reconc-sync.json --digest <plan-digest>
reconc repo sync verify .
```

Read and review every action and blocking issue before apply. Never guess or
recompute the digest. `user-drift`, `orphaned-legacy`, `incompatible`, and
`manual-review` require explicit human or repository-owner resolution; do not
delete the portable receipt or replace user-owned files to force a pass. Apply
revalidates the complete plan under the repository lock, mutates only exact
receipt-owned bytes, and rolls back on failure.

## The Core Decision Loop

Use this canonical repository loop:
```bash
reconc session-briefing . --json
reconc check .
reconc next .
reconc done .
```

### Before Writing
Ultra-terse yes/no (exit 0 = yes, 2 = no):
```bash
reconc can write <path> .
reconc can write <path> . --why      # adds one-line reason on block
```

Full context:
```bash
reconc check . --write <path> --json
reconc check . --write <path> --terse    # ~50-token decision
```

If `decision == "block"`, do NOT write. Read `violations[].recommended_action`.

### After Finishing Work
Execute required commands against the exact staged candidate, then check it:
```bash
reconc exec . --staged -- go test ./...
reconc ci . --staged --json
```

Staged CI accepts successful-command evidence only from an untampered,
unexpired `reconc exec --staged` receipt for the same HEAD and index tree.

Before claiming completion, run the single final gate:
```bash
reconc done .
reconc done . --json
```

The versioned completion report binds the current policy lock, HEAD, index,
worktree, active-session evidence, saved report, current policy result, staged
command proofs, and typed TASK state. A previous explicit block for the same
candidate remains blocking until a later explicit non-blocking check clears it.
Waiting never clears a block. Text mode prints all failed checks and one exact
next action; exit 0 means done, exit 2 means blocked, and exit 1 means the gate
itself failed.

The current v0.9.0 release can export the same candidate as portable JSON or Markdown
reviewer evidence without executing missing commands or persisting a new
policy decision:
```bash
reconc proof . --format markdown --output proof.md
```

Or explicit multi-path check:
```bash
reconc check . --write src/a.go --write src/b.go --command "go test ./..." --json
```

### On Block: Get a Fix Plan
```bash
reconc fix . --write <path> --json
```
The `remediations[].steps` array is ordered, actionable, and scoped.

### Render Human-Readable Explanation
```bash
reconc explain . --write <path> --format markdown
```

## Inspecting Rules

See the full details of one rule:
```bash
reconc why <rule-id> .
reconc why <rule-id> . --json
reconc why mcp .
```

Inspect the compiled state (rule count, sources, digest, warnings):
```bash
reconc doctor .
reconc doctor . --json
```

Versioned session/reentry state:
```bash
reconc session-briefing . --json
```

## Assertions (Claims)

Some rules require explicit sign-offs before writes / session-end:
```bash
reconc hook claim . ci-green
reconc check . --write <path> --claim ci-green --json
```

Claims can also be supplied via an events file, stdin JSON, or by a registered hook integration.

## Platform Integration

The typed registry supports Claude Code, Codex, GitHub Copilot, Cursor,
OpenCode, Devin CLI, Antigravity CLI, Kilo Code, and Grok Build. Run
`reconc hook status . --json`
instead of guessing whether an artifact is installed, configured, degraded,
shadowed, or unsupported. `configured` is static discovery truth, not live
execution proof. Static activation and rate-limited per-route
`expected_events`/`live_events`/`unseen_events` evidence are separate facts.
Treat `loaded`, `observed`, `enforced`, and `inferred` as distinct states:
session/init liveness, exact-route liveness, a negative pre-action proof, and a
weaker host lifecycle respectively.

- **Claude Code**: strongest integration. `PreToolUse` blocks
  protected file edits before execution, `PostToolUse` records reads /
  writes / commands, `Stop` gates session end, and `SessionStart(compact)`
  restores a bounded context packet after compaction.
- **Codex**: session, Bash, `apply_patch`, permission, evidence, and Stop
  hooks. Bootstrap writes `hooks = true` under `[features]`; Codex has no
  `SessionEnd` event. Code-hosted command tools that omit PostToolUse results
  use `reconc exec --staged` for commit-bound success evidence.
- **GitHub Copilot**: `.github/hooks/reconc.json` uses the documented
  version-1 repository contract for Copilot CLI and coding agent. PreToolUse
  and Stop translate to Copilot's exact decision schemas. PermissionRequest
  and Notification are CLI-only, host timeouts remain fail-open, and static
  configuration is not live proof.
- **Cursor**: `.cursor/hooks.json` is shared configuration for Agent/Cmd+K,
  Tab, CLI, and eligible cloud routes, but event delivery is surface-specific.
  `postToolUse` is successful evidence, `postToolUseFailure` is failure only,
  and `afterShellExecution` is passive because it has no exit status.
- **OpenCode**: use `reconc hook install opencode .` for the
  thin project-local `.opencode/plugins/reconc.js` adapter. Shell success
  requires integer `output.metadata.exit == 0`. Bounded asynchronous
  continuation is inferred from `session.idle`, not a synchronous native Stop
  gate.
- **Devin CLI**: `.devin/hooks.v1.json` covers session, user-prompt, tool,
  permission, Stop, cleanup, and post-compaction recovery.
- **Antigravity CLI**: `.agents/hooks.json` covers invocation, tool,
  observation, and Stop events.
- **Kilo Code**: `.kilo/plugin/reconc.js` is the thin CLI/VS Code project
  adapter; `KILO_PURE` must be unset for project plugins to load. It shares
  OpenCode's strict shell-outcome and bounded inferred continuation contract.
- **Grok Build**: `reconc hook install grok .` owns
  `.grok/hooks/reconc.json`. Run `/hooks-trust` once. Native PreToolUse is hard.
  Reconc emits exact Stop block JSON without a leader and probes the installed
  Grok hook guide before assuming synchronous enforcement. User interrupts and
  session-end reasons release. `reconc grok . --prompt "..."` remains the
  explicit ACP path. Passive Stop distributions can use optional leader
  steering over Unix sockets or Windows named pipes. Only delivered
  interjections consume its 32-attempt cap, and capability-proven native hosts
  suppress duplicate prompts. Generator-exact hook/wrapper
  checks and exact route tokens prevent drift. Deep doctor reports native Stop
  support separately and probes protocol 1 plus `_x.ai/interject` for leaders;
  `RECONC_GROK_STEER=0` disables only leader steering.
- **Generic / other agents (Aider, ...)**: invoke the CLI
  directly. `reconc can`, `reconc check --terse`, `reconc next`, and
  `reconc done` are token-optimised for this path.
- **Git**: `reconc hook install git-pre-commit .` drops a pre-commit
  hook that runs `reconc ci --staged` as a hard commit-time backstop.

Do not claim stronger enforcement than `reconc hook status`, the platform
capability contract, and native-shape tests prove. Explicit CLI checks and the
git hook remain the deterministic backstop for unsupported host events.

Configured MCP tools are exact opt-in mappings in `.reconc.yml`. Unknown
identity, wrong fingerprint presence, malformed selected values, unknown
outcome, and `external` effects produce no repository evidence. Cursor's
dedicated pre-hook can deny unclassified MCP calls. OpenCode/Kilo generic hooks
cannot soundly identify unconfigured MCP calls, so strict unclassified deny is
unavailable there. Inspect the compiled redacted contract with
`reconc why mcp .`.

## Autonomous Run Control

The agent operates run control itself. Never ask the user to type Reconc
commands:

```bash
reconc run on .
reconc run status .
reconc run off .
```

Repository mode applies to Claude Code, Codex, GitHub Copilot, Cursor,
OpenCode, Devin CLI, Antigravity CLI, Kilo Code, and Grok Build, scoped to this
repository rather than the whole machine. Claude Code, Codex, GitHub Copilot,
Cursor, Devin CLI, and Antigravity CLI expose synchronous Stop gates. OpenCode and Kilo Code use
inferred `session.idle`, so their host continuation is best-effort and fail-open.
Grok has a native synchronous Stop gate without a leader only when its installed
hook guide advertises blocking Stop decision control. `reconc grok` remains the
explicit ACP path, and passive Stop TUI sessions can be steered via
`_x.ai/interject` over Unix sockets or Windows named pipes.
While typed TASK state is `continue` or `claim`, Reconc returns the host-specific
continuation response without a full terminal policy or Git scan. An empty
active slot with queued executable work yields `claim`; complete or absent
state disables after terminal gates, blocked state reaches terminal Stop, and
invalid state fails closed. PreToolUse, permission, TASK mutation, pre-commit,
and terminal Stop gates remain active.

`run on` first validates live policy sources, the compiled lockfile, and an
executable typed TASK disposition. Resolve its exact remediation before
retrying; use `--force` only as an intentional exception. `run status
--verbose` adds the latest bounded decision without changing the default line
or JSON contract.

`run off` is the only normal manual disable action. Prompt text, runtime interrupts,
session boundaries, runtime changes, and application restarts never mutate the
durable switch; an interrupt releases only the current invocation. Complete or
absent TASK state disables it automatically after terminal gates. Six repeated
no-progress continuations in one session release one Stop without disabling
repository mode or changing another session, preventing an unbreakable host
loop. Reads do not count as progress; TASK changes, writes, and command
outcomes do. Long runs receive bounded policy checkpoints after 64 material
events, 30 minutes with new progress, or a failed command.
Strict Grok Stops bypass the six-event guard and use the separate 32-delivered-
interjection cap. `run reset` is recovery-only for corrupt or foreign-root
state; it preserves the bounded decision log.

## Output Modes (Token Efficiency)

| Mode | Size | When to use |
|---|---|---|
| `--terse` | ~50 tokens | Hook loops, repeated polling |
| `--json` | Full JSON | Agent decision-making, reliable parsing |
| default | Text | Human inspection, logs |

Prefer `--terse` or `can` in hot paths; reach for full `--json` when you actually need the rule ids, explanations, and files-to-inspect.
For session entry and reentry, prefer the compact versioned
`session-briefing --json` contract over separate status and run-status calls.

## Where to Look

- `.reconc/install.lock.json` - portable repository ownership and sync identity
- `.reconc/policy.lock.json` - the compiled lockfile (source of truth at evaluation time)
- `.reconc.yml` - authored config (preset extends, rule overrides)
- `AGENTS.md` - in-prose rules (ingested during compile if `cldc`/`reconc` fenced blocks are present)
- `docs/changelog.md` - rotated automatically if you run `reconc changelog rotate`

## Golden Rules

1. **Never paraphrase policy.** Treat the lockfile / `reconc check` output as authoritative.
2. **Block means block.** Do not write, do not "try anyway".
3. **Warn is a signal, not a suggestion.** Investigate; don't silently ignore.
4. **If a rule seems wrong**, amend the rule (policy PR), don't work around it.
5. **Claims are promises.** Only assert `ci-green` after CI is actually green.
6. **Reconc is not a sandbox.** A hostile same-user process needs external sandboxing and protected remote CI or branch rules.
