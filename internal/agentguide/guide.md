# reconc - Agent Integration Guide

## What reconc Does

`reconc` (Repository Control Compiler) compiles repo policy from YAML / Markdown into a lockfile, then evaluates your proposed actions (writes, reads, commands, claims) against that lockfile. It runs offline, uses no LLM inference, and returns deterministic JSON with a `decision` of `pass`, `warn`, or `block`.

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
| `require_command_success` | Command must succeed | Run it, assert via `--command-success` |
| `forbid_command` | Command is banned | Do not run it; use the suggested alternative |
| `couple_change` | Writes must come in pairs | Edit the paired path(s) in the same change |
| `require_claim` | Workflow sign-off required | Assert via `reconc hook claim <name>` |
| `require_fresh_file` | Artifact must be recent | Regenerate / touch the referenced file |
| `require_evidence` | Text must (not) appear in a file | Update the evidence file to satisfy assertions |
| `require_script` | A script must pass | Run it; fix whatever it flags; re-check |
| `require_assurance` | Native bounded repository gates must pass | Apply the first exact finding, collect real command/proof evidence, then re-check |
| `all_of` / `any_of` / `not` | Composite | Resolve each sub-check; see explanation |

## Bootstrap (New Repo)

Zero-config path:
```bash
reconc bootstrap .
```
One command: scaffolds `.reconc.yml`, compiles, installs git
pre-commit, and installs every registered agent hook whose dedicated
repo-local config directory already exists.

Detect existing conventions and propose matching rules:
```bash
reconc adopt . --yaml       # preview as YAML
reconc adopt . --apply      # append to .reconc.yml (idempotent)
```

Stack-aware policy-pack recommendations are review-only. `adopt --apply` never
adds them to `extends`. Inspect the declared capabilities and select a pack
explicitly only when its inputs, evidence, and gates fit the repository.

Verify installation health end-to-end:
```bash
reconc verify .
reconc hook status . --json
```

After changing policy sources, refresh the generated contract explicitly:
```bash
reconc refresh .
```
Inspection and enforcement commands never compile or write the lockfile.

## The Core Decision Loop

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
Check staged changes against policy:
```bash
reconc ci . --staged --json
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
```

Inspect the compiled state (rule count, sources, digest, warnings):
```bash
reconc doctor .
reconc doctor . --json
```

One-line health:
```bash
reconc status .
reconc run status .
```

## Assertions (Claims)

Some rules require explicit sign-offs before writes / session-end:
```bash
reconc hook claim . ci-green
reconc check . --write <path> --claim ci-green --json
```

Claims can also be supplied via an events file, stdin JSON, or by a registered hook integration.

## Platform Integration

The typed registry supports Claude Code, Codex, Cursor, OpenCode, Devin CLI,
Antigravity CLI, GitHub Copilot, and Kilo Code. Run `reconc hook status . --json`
instead of guessing whether an artifact is installed, configured, degraded,
shadowed, or unsupported. `configured` is static discovery truth, not live
execution proof. Static activation and rate-limited live
`last_seen`/`last_event` evidence are separate facts.

- **Claude Code**: strongest integration. `PreToolUse` blocks
  protected file edits before execution, `PostToolUse` records reads /
  writes / commands, `Stop` gates session end, and `SessionStart(compact)`
  restores a bounded context packet after compaction.
- **Codex**: session, Bash, `apply_patch`, permission, evidence, and Stop
  hooks. Bootstrap writes `hooks = true` under `[features]`; Codex has no
  `SessionEnd` event.
- **Cursor**: `.cursor/hooks.json` covers file, shell, evidence,
  and Stop events exposed by Cursor.
- **OpenCode**: use `reconc hook install opencode .` for the
  thin project-local `.opencode/plugins/reconc.js` adapter. Continuation is
  inferred from `session.idle`, not a synchronous native Stop gate.
- **Devin CLI**: `.devin/hooks.v1.json` covers session, tool,
  permission, Stop, cleanup, and post-compaction recovery.
- **Antigravity CLI**: `.agents/hooks.json` covers invocation, tool,
  observation, and Stop events.
- **GitHub Copilot**: `.github/hooks/reconc.json` returns Copilot-native
  decision JSON and omits its notification-only compaction event.
- **Kilo Code**: `.kilo/plugin/reconc.js` is a thin adapter; `KILO_PURE` must
  be unset for project plugins to load. Like OpenCode, continuation is inferred
  from `session.idle`.
- **Generic / other agents (Aider, ...)**: invoke the CLI
  directly. `reconc can`, `reconc check --terse`, `reconc next`, and
  `reconc done` are token-optimised for this path.
- **Git**: `reconc hook install git-pre-commit .` drops a pre-commit
  hook that runs `reconc ci --staged` as a hard commit-time backstop.

Do not claim stronger enforcement than `reconc hook status`, the platform
capability contract, and native-shape tests prove. Explicit CLI checks and the
git hook remain the deterministic backstop for unsupported host events.

## Autonomous Run Control

The agent operates run control itself. Never ask the user to type Reconc
commands:

```bash
reconc run on .
reconc run status .
reconc run off .
```

Repository mode applies to Claude Code, Codex, Cursor, OpenCode, Devin CLI,
Antigravity CLI, GitHub Copilot, and Kilo Code. While typed TASK state is `continue`
or `claim`, Stop returns the runtime-native continuation response without a
full terminal policy or Git scan. PreToolUse, permission, TASK mutation,
pre-commit, and terminal Stop gates remain active. A blocked, complete, absent,
or invalid TASK plane never receives routine continuation.

`run off` is the only manual disable action. Prompt text, runtime interrupts,
session boundaries, runtime changes, and application restarts never mutate the
durable switch; an interrupt releases only the current invocation. Complete or
absent TASK state disables it automatically after terminal gates. Six repeated
no-progress continuations
release one Stop without disabling repository mode, preventing an unbreakable
host loop. Reads do not count as progress; TASK changes, writes, and command
outcomes do. Long runs receive bounded policy checkpoints after 64 material
events, 30 minutes with new progress, or a failed command.

## Output Modes (Token Efficiency)

| Mode | Size | When to use |
|---|---|---|
| `--terse` | ~50 tokens | Hook loops, repeated polling |
| `--json` | Full JSON | Agent decision-making, reliable parsing |
| default | Text | Human inspection, logs |

Prefer `--terse` or `can` in hot paths; reach for full `--json` when you actually need the rule ids, explanations, and files-to-inspect.

## Where to Look

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
