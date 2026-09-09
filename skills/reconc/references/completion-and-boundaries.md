# Completion and boundaries

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
