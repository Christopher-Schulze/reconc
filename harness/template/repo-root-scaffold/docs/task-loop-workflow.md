# Task Loop Workflow

## Per-TASK Reality-Check Loop (MANDATORY)

After finishing ANY TASK, you MUST run this loop before you advance to the next TASK. It is not optional and not a formality. No TASK is Done until this loop finds nothing left to fix or improve.

1. **Fresh-eyes review.** Review with fresh eyes: strict, paranoid, hard, and forensically deep - as an absolutely merciless, honest, rigorous Reality-Check. Read the changed code LINE BY LINE. Zero guessing. Nothing from memory. No sampling and no spot-checks - explicitly, hard, line by line and goal by goal, critically. Verify every goal and every changed line hard and explicitly.
2. **Interrogate the result, honestly:**
   - Are there any gaps?
   - Is this REALLY, EXACTLY what we wanted - or is it something else? (This has happened often.)
   - Does everything honestly meet our high quality standards? Reference them exactly again - the Hard Quality Mandate in AGENTS.md.
   - Is there anything to fix, or can anything be done more optimally per our quality requirements?
3. **If there is ANY potential work - ALWAYS do it.** Then restart this loop for the same TASK and review again.
4. **Repeat per TASK.** Keep running the loop on the TASK, reviewing again and again, until everything passes this honest, hard Reality-Check and there is nothing left to do. ONLY THEN continue to the next TASK.

## Recording (gated, non-skippable)

The loop is enforced, not advisory. Record its outcome in the `Reality Check Loop` field of the TASK's `## Final Reality Check`, starting with `PASS` and stating the loop ran to completion with nothing left (e.g. `PASS - 2 passes, nothing left`). The `promote-task-done` step that archives the TASK to `docs/tasks/done/` is blocked unless this field is present and asserts completion, so the loop cannot fall under the table between finishing a TASK and the next continuation prompt.
