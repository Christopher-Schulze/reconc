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
| `all_of` / `any_of` / `not` | Composite | Resolve each sub-check; see explanation |

## Bootstrap (New Repo)

Zero-config path:
```bash
reconc setup .
```
One command: scaffolds `.reconc.yml`, compiles, installs git
pre-commit, and installs Claude Code / Codex hooks when their config
directories already exist.

Detect existing conventions and propose matching rules:
```bash
reconc adopt . --yaml       # preview as YAML
reconc adopt . --apply      # append to .reconc.yml (idempotent)
```

Verify setup health end-to-end:
```bash
reconc verify .
```

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
```

## Assertions (Claims)

Some rules require explicit sign-offs before writes / session-end:
```bash
reconc hook claim . ci-green
reconc check . --write <path> --claim ci-green --json
```

Claims can also be supplied via an events file, stdin JSON, or by the Claude Code / Codex hook integration.

## Platform Integration

| Capability | Claude Code | Codex | Generic agents |
|---|---|---|---|
| Pre-write file blocking | Hard for Edit/Write/MultiEdit | Soft self-check | Soft self-check |
| Read/write evidence capture | Hard via hooks | Not file-level | Manual CLI evidence |
| Bash command interception | Hard | Hard for Bash | Manual CLI evidence |
| Stop/final gate | Hard | Hard where hooks run | `reconc done` / git hook |
| Commit backstop | Git pre-commit | Git pre-commit | Git pre-commit |

- **Claude Code**: strongest integration. `PreToolUse` blocks
  protected file edits before execution, `PostToolUse` records reads /
  writes / commands, and `Stop` gates session end.
- **Codex**: Bash-centered hook integration. Command rules can be
  enforced by hooks, but file-level rules still require the agent to
  self-check with `reconc check` before edits.
- **Generic / other agents (OpenCode, KiloCode, Aider, ...)**: invoke
  the CLI directly. `reconc can`, `reconc check --terse`, `reconc
  next`, and `reconc done` are token-optimised for this path.
- **Git**: `reconc hook install git-pre-commit .` drops a pre-commit
  hook that runs `reconc ci --staged` as a hard commit-time backstop.

Do not claim Claude-level file enforcement on Codex or generic agents.
For those platforms, explicit CLI checks plus the git hook are the
deterministic safety net.

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
