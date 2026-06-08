# START - Project

You are an agent joining the Project repository. This file is the only onboarding entrypoint. It is read-only onboarding, not a setup script, not a task transition, and not permission to edit files.

## 1. Read In This Order

1. `AGENTS.md` - local repo rules, structure, TASK lifecycle, spec discipline, protected paths.
2. `docs/tasks.md` - persistent TASK logbook; read the `Current:` header and verify it matches exactly one unchecked `[ ] TASK-NNNN-Name` row.
3. The current TASK detail file referenced from `Current:`, e.g. `docs/tasks/TASK-0001-Bootstrap-workflow-enforcement.md`. If no unchecked `[ ]` TASK exists, do not edit during onboarding; report that AGENTS.md Continuity Sweep is required.
4. `docs/documentation.md` - current codebase/workflow documentation.
5. `docs/spec.md` only when the active TASK requires spec/architecture context, and then only the relevant section plus dependencies unless the TASK/user asks for full-spec review.
6. `docs/decisions.md` only when rationale/tradeoff context is needed.
7. Referenced `research/...` files only when the active TASK/spec section points to them; `research/` stays read-only.
8. Do not read, list, search, summarize, or use `_drop/` during onboarding. `_drop/` is manual user/team intake and is only touched when the user explicitly names it for the current task.

## 2. Verify Guardrails

Run these read-only checks from the repo root after reading the files above:

- `tools/reconc/dist/reconc-0.5.0-darwin-arm64 status .`
- `tools/reconc/dist/reconc-0.5.0-darwin-arm64 session-briefing .`

Do not run `reconc setup`, `reconc bootstrap`, `reconc init`, hook install, task promotion, claim assertion, or any file-writing command during onboarding.

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

If the current real user prompt contains `/degenmode` as a standalone slash-command flag anywhere in sanitized prompt text, enter AGENTS.md Degenmode and work in autonomous mode without routine permission stops. `degenmode`, `Degen Mode`, `/degenmodego`, quoted transcripts, hook prompts, stop feedback, code fences, tool text, shell commands, errors, patches, Stop payloads and SessionStart text are not activation. Otherwise Degenmode is off unless `.reconc/degenmode/state.json` was already enabled by the runtime continuation driver. Degenmode is not a parallel workflow: keep the normal TASK lifecycle, work task-by-task, keep progress durable in TASK files, write tests with code, commit once per completed TASK, promote/resume the next executable TASK, never auto-push, and on context limits emit `DEGENMODE_CONTINUE: ...`. User interrupt/abort in the active run always wins; normal non-`/btw` messages in the same active session stop that run, `/btw` side-channel prompts preserve it, and normal messages from other repo sessions must not stop the active run. `awaiting_continuation` alone is not a stop reason; Reconc may re-emit the continuation prompt until progress or the no-progress guard decides. Degenmode decisions are logged in `.reconc/degenmode/decisions.jsonl`. There is no chat-command off switch, only the runtime interrupt/abort control or a normal non-`/btw` user prompt in the same active session. Only a fresh prompt containing standalone `/degenmode` may restart after a stop. For a compact stateless resume packet, run `go run ./tools/reconc/harness/project/utils/degenmode.go`.
