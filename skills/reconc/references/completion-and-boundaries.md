# Completion and boundaries

## When Policy Is Stale

If `status`, `doctor`, or `check` reports a stale or missing lockfile during
authorized maintenance:

```bash
reconc refresh .
reconc status .
```

Read-only commands never refresh implicitly. Do not hand-edit
`.reconc/policy.lock.json`; it is generated output.
For read-only review, report the state and continue permitted inspection;
do not refresh, initialize, or run completion-state transitions merely because
remediation suggests them. Existing authorization remains valid within scope.

## Agent Behavior

When `reconc` blocks:

1. For the proposed write, run `reconc next . --write <path> --json` to get the
   first typed remediation in one call. For an existing block, read the
   violation and run `reconc next .` to replay it.
2. Honor the action's `kind`, working directory, authorization, and required evidence.
3. Fix the real missing evidence or source issue.
4. Re-run `reconc check . ...`.
5. Finish with `reconc done .`.

When `reconc` warns:

- report the warning if it matters for the user-visible outcome
- do not inflate the workflow unless the warning points to a real missed step

When no policy exists:

- ask whether to bootstrap if repository controls are relevant
- otherwise proceed normally

## Task Completion Boundaries

For a TASK, use its explicit acceptance, required verification, and review of
the changed surface as the completion boundary. Continue only for an
unresolved acceptance defect, a failed required gate, or a necessary fix within
that surface. An optional improvement is current work only when acceptance
explicitly includes it. Record unrelated findings as visible proposals or
queued TASKs instead of silently expanding the current work. A failed gate
requires a real fix and never authorizes weakening evidence; an explicit user
stop pauses the TASK without certifying it. An empty in-scope finding set is a
valid terminal state once the gates pass.

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
