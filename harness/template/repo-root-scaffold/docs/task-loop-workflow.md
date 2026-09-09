# Task Loop Workflow

## Per-TASK Reality-Check Loop (MANDATORY)

After finishing any TASK, run this loop before advancing. It is not optional
or a formality. A TASK is Done only when its explicit acceptance, required
verification, and scoped review of the changed surface all pass. The loop does
not require implementing every imaginable improvement.

1. **Fresh-eyes review.** Review the changed code and stated goals line by
   line: strict, evidence-based, and without guessing. Check the actual
   acceptance criteria, every required gate, and every changed line.
2. **Interrogate the result, honestly:**
   - Is any acceptance criterion unresolved or contradicted?
   - Did every required verification run and pass on the candidate?
   - Does the changed surface meet the Hard Quality Mandate in `AGENTS.md`?
   - Is there a necessary fix within this TASK's stated scope?
   - Is a finding unrelated to the acceptance or changed surface? Record it as
     a separate visible proposal or queued TASK; do not silently expand this
     TASK.
3. **Continue only for real in-scope work.** Fix an unresolved acceptance
   defect, a failed required gate, or a necessary fix within the changed
   surface, then restart the loop. An optional improvement belongs in the
   current TASK only when its acceptance explicitly includes it. A failed gate
   requires a real fix; it never permits bypassing safety, test integrity,
   completion evidence, or the gate itself.
4. **Stop at the bounded terminal state.** When acceptance, required
   verification, and scoped review pass, record the result and advance. An
   explicit user stop pauses the TASK and never certifies it. An empty queue of
   in-scope findings is a valid terminal state; unrelated proposals remain
   visible for later prioritization.

## Recording (gated, non-skippable)

The loop is enforced, not advisory. Record its outcome in the `Reality Check
Loop` field of the TASK's `## Final Reality Check`, starting with `PASS` and
stating that acceptance, required verification, and scoped review completed
with no unresolved in-scope work (for example, `PASS - 2 passes, acceptance,
gates, and scoped review complete`). The `promote-task-done` step that archives
the TASK to `docs/tasks/done/` remains blocked unless this field is present and
asserts completion.
