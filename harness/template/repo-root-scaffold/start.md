# START - Project

You are an agent joining the Project repository. This file is the only onboarding entrypoint. It is read-only onboarding, not a setup script, not a task transition, and not permission to edit files.

## 1. Read In This Order

1. `AGENTS.md` - local repo rules, structure, TASK lifecycle, spec discipline, protected paths.
2. `docs/tasks.md` - persistent TASK logbook; read the `Current:` header. If it is not `Current: none`, verify it matches exactly one unchecked `[ ] TASK-NNNN-Name` row.
3. The current TASK detail file referenced from `Current:`, e.g. `docs/tasks/TASK-0001-Bootstrap-workflow-enforcement.md`. If `Current: none` has queued executable work, report that claim is required after onboarding. If no unchecked `[ ]` TASK exists, do not edit during onboarding; report that AGENTS.md Continuity Sweep is required.
4. `docs/documentation.md` - current codebase/workflow documentation.
5. `docs/spec.md` only when the active TASK requires spec/architecture context, and then only the relevant section plus dependencies unless the TASK/user asks for full-spec review.
6. `docs/decisions.md` only when rationale/tradeoff context is needed.
7. Referenced `research/...` files only when the active TASK/spec section points to them; `research/` stays read-only.
8. Do not read, list, search, summarize, or use `_drop/` during onboarding. `_drop/` is manual user/team intake and is only touched when the user explicitly names it for the current task.

## 2. Verify Guardrails

Run these read-only checks from the repo root after reading the files above:

- `tools/reconc/dist/reconc-darwin-arm64 status .`
- `tools/reconc/dist/reconc-darwin-arm64 run status .`
- `tools/reconc/dist/reconc-darwin-arm64 session-briefing .`

Do not run `reconc bootstrap`, `reconc init`, hook install, task promotion, claim assertion, or any file-writing command during onboarding.

## 3. Report Readiness And Wait

Send one chat message with:

- Current TASK name/description, Status summary, and next `[~]` Sub-Task.
- Files read.
- Reconc status/session-briefing result.
- Dirty worktree summary if visible.
- Open blockers or user-input needs from the current TASK. If no open `[ ]` TASK exists, state `Continuity Sweep required` and summarize whether all logbook rows are checked.

Then stop and wait for the user. No file writes, no claims, no task status changes, no commits during onboarding.

## 4. After The User Says Go

Follow `AGENTS.md`: read the task-relevant source/spec/research paths, edit surgically, update TASK notes/subtasks incrementally, flush `docs/documentation.md` only at real workflow boundaries, run relevant checks, and commit once per completed logical TASK unless the user explicitly says not to.

When autonomous execution is requested, the agent enables the durable switch
itself with `reconc run on .`, verifies it with `reconc run status .`, and
disables it with `reconc run off .` on explicit user stop or a real blocker.
Never ask the user to operate these commands. Repository mode works across all
supported agent runtimes and continues while TASK state is executable. A real
user prompt without `/runloop`, explicit interrupt, or `run off` cancels it;
internal continuation prompts and session end preserve it. It claims queued work when
`Current: none`, releases terminal or blocked state to the hard Stop gate, and
fails closed on invalid TASK state. The older standalone `/runloop` prompt
remains session-scoped compatibility. Run control is not a parallel workflow:
keep TASK progress durable, write tests with code, commit once per completed
TASK, promote or claim the next executable TASK, never auto-push, and on
context limits emit `RUNLOOP_CONTINUE: ...`. Explicit user interrupt always
wins. For a compact stateless resume packet, run
`go run ./tools/reconc/harness/project/utils/runloop.go`.
