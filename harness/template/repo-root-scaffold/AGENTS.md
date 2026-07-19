# Project - Local Agent Instructions

## Scope

This file is the local repository override for the target project. It complements global agent rules and MUST NOT duplicate them unless this repo needs a stricter or different rule.

Never edit, normalize, rename, delete, or "clean up" global agent files from this repo context. In particular: `~/.codex/AGENTS.md`, `~/.cursor/`, `~/.config/opencode/AGENTS.md`, any `.codex/AGENTS.md`, and global Codex/Cursor/OpenCode skills/config stay untouched unless the user explicitly asks for that exact global file.

Chat language is German: every normal chat message to the user must be in German unless the user explicitly asks for another language or the output is a quoted/source artifact that must preserve its original language. Repository docs and code should stay in the language/style already used by the touched file; for `docs/spec.md` keep the current dense technical style.

Protected local paths: `research/` is read-only reference material and must never be edited, normalized, moved, deleted, or cleaned unless the user explicitly asks to manage research assets. `_drop/` is a git-tracked manual intake zone for user/team-provided files; agents must not read, list, search, summarize, index, move, edit, delete, cite, or use its contents as context unless the user explicitly names `_drop/` for the current task. `README.md` is a static private GitHub team info page, not source-of-truth; agents must not read, analyze, summarize, cite, or edit it unless the current user instruction explicitly names README work. This `AGENTS.md` is also read-only after bootstrapping; edit it only when the user explicitly asks to change local agent instructions.

Output discipline: keep chat/token overhead low. Do not narrate routine Reconc/status/cache/compile steps or every tiny operational action; run them silently and report only task-relevant results, blockers, real workflow friction, safety issues, important verification failures, or decisions the user should know. Keep lightweight background awareness of workflow friction, but do not spend tokens on it unless a real issue appears; then inspect deeper and report the concrete evidence + best fix. Intermediary updates should be short and useful, not a running log; final reports should state what changed, what passed, what remains, and the commit, without generic process chatter.

## Standalone Working Contract

These local instructions must be sufficient for agents that do not have the user's global Codex `AGENTS.md`.

- Read-before-act: never guess paths, APIs, signatures, return types, file contents, repo state, or task state. Read the source/config/doc first.
- Full-scope literalism: "all", "every", "each", and implied totality mean every single item, no sampling.
- Production-grade only: no stubs, fake logic, placeholders, skeletons, TODO bodies, always-green tests, or mocked-out mainline behavior.
- Existing patterns first: search for existing modules, helpers, schemas, interfaces, and naming before adding new ones.
- No parallel systems: extend the existing project subsystem when possible instead of creating a second mechanism.
- No partial-as-done: if blocked or incomplete, state exactly what is done, what is open, and why.
- Complexity surprise: if the task becomes much larger than scoped or needs a migration/destructive change, stop and report before continuing.
- Post-implementation verify: reread modified files, run relevant checks/tests, confirm requirements, stale names, and doc/task updates.
- Error-loop recovery: first failure retry with a concrete tweak; second failure step back and inspect root cause; third failure stop and report alternatives.
- Commits: when user asks for edits in this repo, commit after the completed logical change unless they explicitly say not to.

## File Operations

- Existing files are edited surgically in place. Do not delete/recreate or full-rewrite a file just because that is faster.
- Before editing a file: read the relevant section; for broad rewrites or high-risk docs, read the full file.
- Manual edits use `apply_patch`. Bulk mechanical rewrites are allowed only when the transform is obvious and verified before/after.
- Never overwrite user work. If unrelated dirty changes exist, leave them alone. If touched-file changes conflict with the task, inspect and work with them.
- Never use destructive git commands (`reset --hard`, `checkout --`, branch deletion) unless explicitly requested.
- Moving content: create target, verify content was preserved, then remove source only when the user requested/approved the move.
- Deleting files: only with explicit user request, except build artifacts/caches that are clearly generated.
- Remote files: download/read/edit locally/upload/verify. Do not remote-`cat >`, remote-`sed -i`, or remote-heredoc existing files.

## Test Integrity

- Tests must verify real behavior of real code and must be capable of failing.
- Never weaken, skip, delete, or rewrite a test to hide a product bug.
- Do not use assert-true, hardcoded fake outputs, bypass mocks, or empty test bodies.
- If a test fails, root-cause whether code or test is wrong. Change the test only when the test is genuinely incorrect and state why.
- For Go changes, run the narrow relevant test first, then broader `go test ./...` when feasible.
- Code and tests are one deliverable: every TASK that writes product/tooling/runtime code must add or update substantive tests in the same TASK, before Done. Target 100% meaningful coverage for changed behavior: happy path, denied/error path, edge cases, persistence/config drift, concurrency/lifecycle and degraded/offline behavior where relevant. Uncovered code is allowed only when technically impossible or user-directed, and then the TASK Final Reality Check must state the exact gap, risk, and follow-up/deviation.
- Test placement is canonical: Go tests are co-located `*_test.go` in the same package as the code; durable script/tooling tests live beside the Go tool under `codebase/scripts/{audits,generators,utils,...}`; frontend tests live inside the owning `codebase/frontend/...` surface using the repo Bun workspace once that workspace exists. No ad-hoc root `tests/` folders.

## AI Workflow

- Use `rg`/`rg --files` first for search. Prefer direct file reads for known paths.
- Batch independent reads/searches in parallel where possible.
- Do not spawn sub-agents unless the user explicitly asks for agents/delegation or the task is clearly split into independent parallel work and delegation is allowed by the current instructions.
- Keep edits small and reviewable. Large migrations should be split into explicit TASKs.
- For reviews, lead with concrete findings and file/line references, ordered by severity.
- For implementation, keep moving until the logical task is done: edit, verify, commit if appropriate, final report.
- If context is compacted or resumed, re-verify current task, modified files, and latest user instruction before finalizing.

## Agent Execution Hardening

Agent progress is not proof of correct runtime mode, workflow state, or implementation completeness. Any agent claiming autonomous continuation, hook-enforced workflow, TASK Done, or "clean" state must verify the real local state first: `reconc run status .`, current `docs/tasks.md`, active TASK detail, relevant hook output or runtime state, Reconc checks, and `git status --short --branch -uall`.

For every TASK, every agent must:
- Read the active TASK detail and touched implementation surface before editing.
- Treat implemented-looking code as suspicious until production behavior is proven by execution, tests, or direct source evidence.
- Actively hunt fake-complete code: ignored variables, no-op branches, exact-match logic pretending to be regex, zero-value timeout traps, weak validation, map-order nondeterminism, placeholder comments, unchecked errors, non-awaited goroutines/processes, non-persisted "persistence", and wired-but-never-executed paths.
- After implementation, re-read every touched file and perform a fresh-eyes failure pass: what still breaks in production, what is only tested accidentally, what remains nondeterministic, what silently degrades, what can race, and what can be bypassed.
- Run narrow tests for touched packages, race tests for concurrency/state/persistence code, broad backend tests for shared behavior, and Reconc task-state/check gates before Done.
- Before marking Done, verify `git status --short --branch -uall`; no untracked or dirty paths may remain unless explicitly documented as unrelated user work.
- Never mark Done with stale `Current`, missing next `[~]` Sub-Task, missing TASK detail move, failed Reconc audit, untracked files, placeholder logic, or tests that only prove mocks.
- While Repository Run is active: after a TASK is Done, commit once, promote the next executable TASK, and continue immediately. Never stop after an arbitrary number of TASKs.

## Repository Run

`reconc run on .` enables repository-scoped autonomous continuation and
`reconc run off .` disables it. The agent operates this switch itself; never
ask the user to run these commands. Inspect durable truth with `reconc run
status .` and bounded transition history with `reconc run log .`. Repository
mode works through Claude Code, Codex, Cursor, OpenCode, Devin CLI,
Antigravity CLI, Kilo Code, and Grok Build, scoped to this repository rather
than the whole machine. Claude Code, Codex, Cursor, Devin CLI, and Antigravity
CLI expose synchronous Stop gates. OpenCode and Kilo Code use
inferred `session.idle`, so their host continuation remains best-effort and
fail-open. Grok's native PreToolUse gate is hard, and Grok 0.2.106+ also
enforces Reconc Stop blocks without a leader. Older Grok versions use
`reconc grok . --prompt "..."` or optional leader fallback for strict
same-session continuation. It survives internal continuation prompts, compaction, session
boundaries, and model restarts. A runtime interrupt releases only the current
invocation. Prompt text, interrupts, session lifecycle events, runtime changes,
and application restarts never mutate durable run state. `run off` is the only
manual disable action.

Repository mode is not a second workflow. Read `Current:`, execute the active
Sub-Task, write same-TASK tests, run checks, update docs and TASK truth,
complete Final Reality Check, archive and commit once, promote or claim the
next executable TASK, then continue. An executable current TASK yields
`continue`; queued executable work with no current TASK yields `claim`;
blocked-only, complete, or absent TASK state reaches terminal Stop; malformed
or ambiguous TASK state fails closed. Routine executable continuation skips
the full Stop report and Git scan, but PreToolUse, TASK mutation,
pre-commit, invalid TASK state, and terminal Stop remain hard gates.

After repeated Stop events without typed TASK, write, or command progress,
repository mode releases one Stop and resets the guard without
silently changing its durable switch. Run decisions are written only for
material transitions to the bounded `.reconc/run/decisions.jsonl` ring.
Reads do not fake progress. Repository mode runs a full policy checkpoint only
after 64 material events, 30 minutes with new material progress, or a failed
command; routine executable Stops remain Git-free.
If nearing context or tool limits, persist exact progress in the active TASK.

**Autonomous Non-Interactive Rule:** While run control is active, do not ask
the user for routine direction, confirmation, or permission and do not pause
at arbitrary checkpoints. Stop only on explicit user stop, destructive or
high-risk choice requiring authority, missing credentials or rights, external
access blocker, unresolved failing tests/build after root-cause attempts,
policy conflict needing user direction, or the verified zero-finding Terminal
Gate. Do not auto-push. Do not touch `_drop/` or `research/` unless explicitly
instructed.


## Source Of Truth

Use these files in this order:
- `docs/spec.md` - final technical target-state blueprint; implementation contracts live here.
- `docs/documentation.md` - SSOT for feature/API/domain/setup/architecture docs outside target-state spec.
- `docs/decisions.md` - rationale, tradeoffs, rejected alternatives, extracted why-context.
- `docs/deferred.md` - parking lot for intentionally deferred ideas/features that may be revisited later.
- `research/` - read-only reference implementations and source floors used by the spec.

If these conflict: `docs/spec.md` wins for target architecture, `docs/decisions.md` wins for rationale, global Codex rules win for generic workflow/safety unless this file is stricter for this repo.


## Reality Check Standard

### Hard Quality Mandate (per TASK, non-negotiable, no softening)

This operationalizes the quality bar as a hard per-TASK gate. Every TASK is brought to this bar before it is Done. No "good enough", no "for now", no Gaps, no fake-complete. The work has to be excellent.

- Brutal efficient, brutal performance-optimized, brutal efficiency-optimized; deterministic fast-paths before any expensive/LLM call; no wasted work, allocation, latency, CPU/memory, or inference.
- Maintainable and secure by default: deny-by-default, fail-closed, auditable, privacy-preserving, clean and AI-readable.
- NO Gaps, nothing forgotten: implement every spec atom of the listed range, or explicitly own the remainder via a concrete follow-up TASK. Never declare `NO_SPEC_SURFACE` without grepping `docs/spec.md` first; never squat a spec-reserved path/name for an unrelated feature.
- Maximum leverage per feature: pull the full intended effect of each feature, truly maxed-out, innovative where it creates materially better efficiency/effectiveness/reliability/performance. Not the smallest runnable approximation.
- Integrate into existing project subsystems; never build a parallel/duplicate/shadow system. Grep for the existing mechanism and extend it; reuse > reinvent.
- Plan precisely and self-control strictly: before coding read the relevant `docs/spec.md` section and every `research/...` ref (research is a floor, not inspiration) and adapt it to at least the same practical depth, then improve it.
- After every TASK do a real, honest Reality Check and self-reflection: is this truly perfectly done? Final Reality Check + Contradiction Check with concrete `file:line` evidence and the exact test output. Hollow prose is rejected.
- Verify goal by goal, atomically: prove each acceptance atom individually with real evidence. No sampling, no spot-checks, no "probably fine" - every single item, literally.
- Exactly one commit per TASK including `git rm` of the archived task path; never bundle TASKs, never leave the worktree dirty, never stack uncommitted work.

### Per-TASK Reality-Check Loop (MANDATORY)

Full workflow: `docs/task-loop-workflow.md`. After finishing ANY TASK you MUST run this loop before advancing to the next TASK; no TASK is Done until the loop finds nothing left to fix or improve.

1. Fresh-eyes review: strict, paranoid, hard, forensically deep, as an absolutely merciless, honest, rigorous Reality-Check. Read the changed code LINE BY LINE. Zero guessing, nothing from memory, no sampling and no spot-checks - explicitly, line by line and goal by goal; verify every goal and every changed line hard and explicitly.
2. Interrogate honestly: any gaps? Is this REALLY, EXACTLY what we wanted or something else (this has happened often)? Does everything meet our high quality standards (the Hard Quality Mandate above)? Anything to fix or do more optimally per our quality requirements?
3. If there is ANY potential work - ALWAYS do it, then restart the loop for the same TASK and review again.
4. Repeat per TASK until everything passes this honest, hard Reality-Check and there is nothing left to do. ONLY THEN continue to the next TASK.

Every proposal, spec edit, architecture change, code path, optimization, and research import must survive a real-world usefulness check.

Accept only changes that measurably or clearly improve at least one of: task success, reliability, latency, cost, security, compliance, auditability, debug time, operator clarity, maintainability, scalability, fault tolerance, integration quality, reduced LLM inference, reduced misuse, reduced silent failure, repeatability, or testability.

Reject changes that are only cosmetic, duplicated by existing spec mechanisms, abstract best practice, complexity without leverage, parallel systems where an existing project subsystem can be extended, or theoretical wins that will not matter in operation.

End-of-TASK Reality Check is mandatory before any TASK is marked Done. It must verify actual implemented behavior, test/check evidence, operator/customer usefulness, complexity drawdown, and whether the result still matches the target architecture. Passing prose without evidence is not enough.

Spec parity rule: `docs/spec.md` is a minimum bar, not a ceiling. Implementation must satisfy all applicable spec contracts, security/privacy boundaries, path/layout rules, runtime constraints, and research-reference obligations; it may exceed the spec when the result is clearly better, but better work is never rolled back merely to fit an older spec. If an implementation changes target-state behavior beyond the spec, update `docs/spec.md` in the same TASK when the user explicitly allows spec editing; otherwise preserve the better implementation, record the user-directed exception in the TASK Final Reality Check/Deviations, and queue a spec-alignment TASK if parity cannot be restored immediately.

User direction wins inside safe/project boundaries: if the user explicitly steers scope, implementation shape, or temporary spec drift, document it and follow it; do not silently reinterpret user intent as a generic best-practice gate.

## `docs/spec.md` Discipline

`docs/spec.md` is the final technical blueprint. It describes only the final target state. No roadmap, no history, no migration narrative, no "before/after", no TODOs, no implementation chronology.

Spec content rules:
- Markdown only: no fenced code blocks, no pseudocode blocks.
- Interfaces/structs/signatures: Markdown table or dense inline field list.
- SQL/data schemas: Markdown table plus index bullets.
- Algorithms: numbered steps or compact arrow chain.
- Config/defaults/thresholds: table or inline `field=default`.
- Metrics/events/flags: table or dense inline metric list.
- No market analysis, rejected alternatives, rationale paragraphs, customer anecdotes, or review notes in spec; move that to `docs/decisions.md` if it must be preserved.
- No placeholders, stubs, open questions, removed markers, deferred markers, or phase markers unless the spec explicitly points to `docs/deferred.md`.

Compaction rule: maximum technical information per line with zero information loss. Combine small tables, inline small lists, remove intro prose, keep synergies/metrics close to the feature they belong to.

Before editing `docs/spec.md`: read the relevant section and surrounding dependencies. For broad architecture/naming work, read the full file or prove the search surface is complete.

After editing `docs/spec.md`: run targeted searches for stale naming, broken syntax, and touched research references. If any `research/...` reference count in touched scope decreases accidentally, stop and restore it.

## Research References

Research references are implementation pointers, not decoration. They tell the coding agent what source code to study before implementing a feature.
This AGENTS section only defines the non-negotiable reference-safety rules.

Preserve every `research/...` reference in `docs/spec.md` unless the user explicitly removes the entire feature and confirms the reference is no longer needed.

Valid reference forms include:
- Full paths such as `research/agents/example-main/path/file.go`
- `Referenz:` / `Reference:` lines
- Inline pattern labels tied to a repo
- Parenthetical refs
- Comments or bullets that point to research code

Path format must stay greppable: `research/{category}/{repo-name}/path/to/file`. Do not shorten to repo-relative paths.

Implementation workflow for referenced features: read the relevant spec section -> read referenced source paths -> deep analysis/reality-check/output shape -> rebuild/adapt in project style using existing subsystems instead of parallel systems -> improve beyond the reference where the project architecture gives leverage.

Research folders are read-only unless the user explicitly asks to manage research assets.

## Glossary

- Root: repo root you name, we are located in it, NEVER do/touch anything directly in CODE/ - only in its subfolders (project folders/repos).
- TASK: discrete, user-visible deliverable (number/slug/path: see Repo Structure; format: TASK Lifecycle).
- SUB-TASK: work step within a TASK, as bullet in parent TASK file.
- FLUSH: propagate TASK-relevant changes into persistent docs (documentation.md; on TASK Done additionally detail file to tasks/done/).

## Repo Structure (SSOT)

Roots:
- New/empty repo default is flat-root: `AGENTS.md`, `README.md`, `.gitignore`, `docs/`, `research/`, `tools/`, `backend/`, `frontend/`, `db/`, `config/`, `assets/`, `scripts/`, `_drop/` as needed. Hidden tool dirs may exist but are not architecture.
- Existing repos win: if a repo already has a coherent structure, analyze it and adapt Reconc to it instead of reshaping the repo.
- `docs/` - project docs/spec/concept/product docs.
- `research/` - read-only reference repos and analysis artifacts.
- `tools/` - operative repo tools used by the workflow, e.g. `tools/reconc/`; not product runtime code.
- `codebase/` is supported but not the default. If present, product source/assets/config/scripts live below it via the dual-path Reconc audits.
- `_drop/` - manually reviewed intake folder for files from the user/team; git-tracked but excluded from autonomous agent context/policy traversal unless explicitly requested.
- On-demand rule: reserved subfolders are created only when the first real file for that category exists. Never create empty placeholder trees.

Supported owner paths:
- `backend/` or `codebase/backend/` - backend source and binary entrypoints for the selected stack.
- `frontend/` or `codebase/frontend/` - frontend workspace only when the selected stack needs it.
- `db/` or `codebase/db/` - DB schemas, migrations, seeds, fixtures, snapshots and sample DBs when the selected stack needs durable storage.
- `config/` or `codebase/config/` - product config, policy and architecture rules.
- `assets/` or `codebase/assets/` - embedded/static product assets.
- `scripts/` or `codebase/scripts/` - durable build/test/audit/generator/maintenance tooling.
- `build/` or `codebase/build/` - generated build output, ignored.

Docs (default: everything in documentation.md):
- docs/documentation.md - SSOT of all docs (IN/NOT-IN + inner structure: see Project Bootstrap).
- docs/tasks.md - complete chronological TASK logbook, 1 line per TASK from project start.
- docs/tasks/TASK-NNNN-Name.md - open TASK detail, 1/TASK.
- docs/tasks/done/ - archive of completed TASKs (replaces classical changelog.md).
- docs/spec.md - optional (see spec.md Discipline).
- docs/decisions.md, docs/deferred.md - optional side-cars for spec.md.
- Dedicated files outside documentation.md only for standard filenames with justified reason (spec.md, decisions.md, deferred.md). No ad-hoc new doc files.
- docs/ filenames: lowercase except TASK detail/archive files, which MUST keep exact `TASK-NNNN-Name.md` Title-Case task names under `docs/tasks/` or `docs/tasks/done/`.
- README.md: max 1 at repo root; static private GitHub team info page only, protected like `_drop/` for autonomous agents. Do not read/use/edit it unless the current user instruction explicitly requests README work.

Scripts (creation rules: see Project Bootstrap):
- `scripts/benchmarks/` or `codebase/scripts/benchmarks/` (never benches/).
- `scripts/tests/` or `codebase/scripts/tests/` - test orchestration scripts only when the stack needs it.
- `scripts/build/`, `scripts/generators/`, `scripts/migrations/` or their `codebase/` equivalents plus repo-local workflow harness `tools/reconc/harness/project/{audits,utils,config/workflow}/`.

TASK IDs: `TASK-NNNN-Name`, 4-digit global ascending from `TASK-0001`, never reused; `Name` is short dash-separated Title Case; filename pattern `TASK-NNNN-Name.md`.

Naming (pointers to content owners):
- Scripts/utils/benchmarks: see Tooling Policy.
- Language-specific code and folder layouts are selected during bootstrap from the existing repo or user-confirmed stack.

Existing projects: adopt existing structure + naming 1:1; never enforce own structure. Full rules: Project Bootstrap / Existing Projects.

## Project Bootstrap (one-time, non-destructive, never overwrite)

(Paths, file list + folder layout: see Repo Structure.)

Create-if-missing in docs/:
- documentation.md (SSOT for all documentation):
  - IN: feature descriptions, API surface, domain concepts, usage guides, setup/config, system architecture (layer structure, data flow, key components, IPC wiring, external integrations, structural WHY decisions - as own H2 section(s)).
  - NOT IN: TASK history (-> tasks/done/), code implementation details (-> inline), file inventory (agent uses filename-pattern/content-search/ls; manually maintained always goes stale).
  - One topic = one place, never duplicates.
  - Inner structure: H1 title, TOC as section list at top, H2 sections per topic/feature (alphabetical or by feature area), each section slug-anchor-compatible.
- tasks.md, tasks/, tasks/done/ (format: see TASK Lifecycle).

Codebase/script subfolders are on-demand: create only when the first real asset of that category exists. No fitting category: name new category clearly + document in documentation.md. No new folder before first file.

Existing Projects: at session start, analyze current state + adopt. No renames/moves/duplicates. NEVER enforce own structure, NEVER auto-migrate, NEVER delete what we inherit. (changelog.md/todo.md/context.md/map.md) - agent serves what's found instead of introducing its own. Existing main doc = canonical via alias. Report conflicts + gaps to user (what's missing, where own structure would compete). User decides: adopt, migrate, or introduce additionally. Without explicit user decision: adopt as found. Introduce own structure only where no competitor exists + genuine gap.

## Tooling Policy

- Existing project tooling wins. Do not introduce a new toolchain when the repo already has a working pattern.
- Durable repo tooling follows the selected stack and existing repo conventions. The Reconc harness itself stays under `tools/reconc/`.
- Bash is allowed only for local one-liners or tiny bootstrap glue when the selected stack cannot run yet; do not leave durable project logic in shell scripts unless the repo already uses that pattern.
- Scripts live under `scripts/{benchmarks,tests,audits,build,utils,generators,migrations}/` by default or `codebase/scripts/{...}` when the repo uses the `codebase/` layout; no empty scaffolds.
- Script naming: kebab-case, action-first, intention-revealing, same category uses same structure.
- Repeatable build/test/maintenance action introduced by a task should get a script instead of staying as one-off tribal knowledge.
- Do not add Docker, Node services, Python runtime tooling, or external daemons for core project paths unless the spec, selected stack, or user explicitly requires it.
- Frontend dependency locality is stack-specific. If a Bun/TS frontend is selected, keep one frontend workspace and one ignored `node_modules/` tree.

## TASK Lifecycle

> **`docs/tasks.md` is APPEND-ONLY — hard invariant.**
> Allowed mutations only: (1) append a new `- [ ] TASK-NNNN-Name - <desc> -> tasks/TASK-NNNN-Name.md` row at the end, (2) on Done flip `[ ]` to `[x]` AND swap `tasks/` to `tasks/done/` for that one row, (3) edit the single `Current:` control line.
> Forbidden: delete any row, multi-row delete, full-file rewrite, truncate history, reorder existing rows, regenerate the file from scratch.
> Violation = data destruction of the project logbook. Reconc enforces this via the `project-tasks-md-rows-immutable` audit; bypassing the audit (`--no-verify`, claim override) is allowed only when the user explicitly directs the deletion.

(Files + ID pattern: see Repo Structure.)

Task logbook icons: `[ ]`=open/current-or-future, `[x]`=done. Detail-file Sub-Task icons: `[ ]`=open, `[~]`=current, `[x]`=done.

tasks.md: flat permanent logbook, no sections, append-only row order. `Current: TASK-0001-Name -> tasks/TASK-0001-Name.md` is the fixed mutable control line before all TASK rows; `Current: none` is valid only when no TASK is Active, including blocked-only and terminal state. The line is always present and intentionally edited on activation, hot-switch, resume, blocker release, and terminal archive. A non-empty `Current:` points to exactly one unchecked `[ ]` row and names the active/next TASK; it does not have to be the first unchecked row, because blocked/paused work and hot-switches can leave earlier open rows waiting. Every TASK appears exactly once from project start with checkbox, exact task name, compact description, and matching detail-file link. Line format: `- [ ] TASK-0001-Name - Description -> tasks/TASK-0001-Name.md` or `- [x] TASK-0001-Name - Description -> tasks/done/TASK-0001-Name.md`; `[ ]` rows not referenced by `Current:` are queued/blocked/paused future work, and historical row order is append-only except `[ ]->[x]` plus `tasks/->tasks/done/` on Done.

Queue order: row order is execution priority. `Current:` must point to the earliest executable `[ ]` TASK in row order. A preceding `[ ]` row is allowed only when its detail `State` is `Blocked|Paused` or its `Depends On` tasks are not Done. When adding many TASKs, order them for maximum implementation efficiency: foundational architecture/contracts before product surfaces, shared infrastructure before module-specific work, security/policy/audit before autonomous execution, tests/gates beside the risky system they protect, thematically adjacent tasks together. Use dependency fields instead of prose to defer work.

Parallel work: TASKs may be parallelizable only when their `Scheduling` section has the same `Parallel Group: PG-Name`, no dependency edge between them, disjoint `Expected Touch Surfaces`, and the user explicitly allows parallel/sub-agent work. `Expected Touch Surfaces` is a comma-separated list of repo-relative owner paths/globs that the TASK is expected to edit (for example `backend/project/internal/policy/**, config/policy/**`); use narrow owner surfaces, not root-wide globs, and update it before implementation if the real touch set changes. `Current:` still names one primary task; parallel groups are planning metadata and must not override dependency/order checks.

Task size: medium by default. Do not make giant epics that hide risk, and do not create micro-tasks for tiny chores; split only when a TASK cannot be finished/reviewed/committed as one coherent deliverable.

Detail file: filename and H1 are the exact task name (`# TASK-0001-Name`) + H2 sections `Why`, `Status`, `Scheduling`, `Technical Plan`, `Acceptance`, `Sub-Tasks`, `Notes`, `Deviations`. `Status` first line is `State: Active|Queued|Blocked|Paused|Done`; only the `Current:` TASK may be `Active`, queued open TASKs use `Queued`, interrupted work uses `Paused`/`Blocked`, archived work uses `Done`. `Scheduling` must contain bullets `Priority: P0|P1|P2|P3`, `Depends On: none|TASK-0001-Name, TASK-0002-Name`, `Parallel Group: none|PG-Name`, `Expected Touch Surfaces: path/glob, path/glob`, `Order Rationale: <why this task sits here>`; dependencies must point to earlier logbook rows, and same-`PG-*` tasks must not have overlapping touch surfaces. `Technical Plan` contains the detailed implementation/research/audit plan; `Sub-Tasks` is the fine-grained progress checklist with `[ ]/[~]/[x]` and exactly one `[~]` in the current TASK; non-current open TASKs have no `[~]` and at least one `[ ]`. `Notes` is allowed and expected to hold status updates, findings, remarks, context, commands, blockers and resume notes. Before Done add `## Final Reality Check` with exact fields: `Spec Parity`, `Spec Scope`, `Reality Check`, `Reality Check Loop`, `Evidence`, `Beyond Spec Handling`.

Session start: run `reconc run status .` -> read tasks.md -> read `Current:` header -> if non-empty, verify it points to exactly one unchecked `[ ]` row -> open its detail file -> continue at its `[~]` Sub-Task using `Status`, `Technical Plan`, `Notes`, and `Sub-Tasks`. If `Current: none` has queued executable work, claim it. If there is no `[ ]` row, run Continuity Sweep instead of stopping. Verify env vs stack.

New TASK Initial Sweep: read key files for structure/patterns/deps/naming/interfaces/constraints -> findings to Notes, no edits -> plan Sub-Tasks. Ambiguous scope -> announce interpretation. Large/destructive -> await confirmation.

Execution: Sub-Tasks incremental, [~] on current. Tests are written with the code in the same TASK, not postponed. Build+tests after each logical Sub-Task + before Done. Unit/Integration/E2E per significant path. Fail -> fix root cause + update Notes. Pre-build: validate cache else toolchain cleanup.

Workflow Friction Reporting: keep this as low-overhead background awareness, not a parallel audit. Do not run full workflow investigations or explain routine Reconc mechanics unless there is a real signal. If onboarding, TASK state, Reconc policy, hooks, `Current:` selection, touch-surface planning, docs flush rules, commit gates, or spec-parity checks become ambiguous, brittle, noisy, too weak, too strict, or likely to cause drift, then inspect deeper and report exact evidence with the best-fix proposal; otherwise say nothing about workflow hygiene and stay focused on the user task. If the fix is small and user-authorized, implement it surgically, verify it, record it in the active TASK Notes, and commit when requested.

Refactor/Replace: new fully subsumes old -> propagate all refs/docs/tests -> remove old. No v2/final/optimized parallel files, refactor in place. Exported-API change: grep all callers + atomic update in same change.

Propagation per change: (1) update importing/referencing files; (2) documentation.md if feature/API/domain/usage/setup or architecture (layer/wiring/integration/structural-decision) touched+outdated; (3) write script if repeatable task/build-step/test/maintenance op introduced without script - now, not later.

Flush triggers (update documentation.md immediately if touched): 5 Sub-Tasks without flush; 30min since last; pre-build/test; pre-session-end/tool-shutdown.

TASK Done: all Sub-Tasks [x] + Acceptance met + changed code has same-TASK substantive tests + tests green + build clean + no unresolved deps/redundancies + `## Final Reality Check` complete. Final Reality Check must state `Spec Parity` as one of `MATCHES`, `EXCEEDS_SPEC_UPDATED`, `EXCEEDS_USER_ACCEPTED_NO_SPEC_EDIT`, `NO_SPEC_SURFACE`; `Spec Scope` names exact spec sections or says no spec surface touched; `Reality Check` starts with `PASS -` and names real benefit/drawdown; `Reality Check Loop` is mandatory and starts with `PASS` and confirms the per-TASK Reality-Check loop in `docs/task-loop-workflow.md` was actually run to completion with nothing left (e.g. `PASS - 2 passes, nothing left`); the `promote-task-done` step that archives the TASK is blocked unless this field is present and asserts PASS, so the loop cannot be skipped between finishing a TASK and continuing to the next; `Tests` names the test files/commands/coverage proof or explicitly says `NO_CODE_CHANGED`; `Evidence` names commands/files/manual proof; `Beyond Spec Handling` says `N/A`, spec updated, or user-directed exception/follow-up. Then update documentation.md if touched -> finalize detail (Why/Acceptance/Sub-Tasks-log/Notes/Deviations/Final Reality Check complete) = this TASK's changelog entry, no separate synthesis -> move detail to `docs/tasks/done/TASK-NNNN-Name.md` -> tasks.md row `[ ]->[x]` and target `tasks/` -> `tasks/done/`; the completed row stays permanently visible.

New TASK: append a new `[ ] TASK-NNNN-Name - Description -> tasks/TASK-NNNN-Name.md` row with number=max+1; create matching detail with Why/Status/Scheduling/Technical Plan/Acceptance/Sub-Tasks/Notes/Deviations. `Scheduling` must include real `Expected Touch Surfaces` before the TASK is accepted. No stock planning; the plan must be concrete enough that a fresh agent can resume without chat history. Do not renumber, reorder, or insert rows between historical tasks after commit; before commit, reorder only newly added rows to get the best dependency/efficiency sequence.

Continuity Sweep (no open `[ ]` tasks): set `Current: none`, reread this AGENTS.md TASK Lifecycle, `docs/tasks.md`, `docs/documentation.md`, and `docs/spec.md` completely; inspect relevant product docs and current repo structure; then do a hard Reality Check to find remaining valuable work. Create new TASKs only for real gaps that improve implementation readiness, spec parity, architecture consistency, testability, product value, security, performance, reliability, or maintainability. Scope must be medium-sized: not tiny chores, not giant epics; each TASK gets a conforming detail file with concrete Why, measurable Acceptance, actionable Sub-Tasks, Notes, Deviations. Append enough `[ ]` rows to keep work flowing but do not flood the logbook; set `Current:` to the first appended `[ ]` row. If the project truly appears complete, do not invent fake work: keep `Current: none`, report only the zero-finding Terminal Gate status defined by the project completion workflow with evidence, and wait for explicit user confirmation.

Discovered issue: belongs to current TASK -> Sub-Task or Notes; separate work-item -> append a new `TASK-NNNN-*` logbook row + detail file after the current planned work, don't start now unless user reprioritizes it.

Hot-Switch (urgent mid-TASK): keep logbook append-only; set the interrupted TASK `Status` to `State: Paused`, remove its `[~]` by marking the paused sub-step `[ ]`, add a pause/resume note, append the hotfix as `TASK-NNNN` with `State: Active`, and set `Current:` to the hotfix. After hotfix Done, restore the paused TASK to `State: Active`, mark exactly one resume sub-task `[~]`, and set `Current:` back to it.

TASK-Split (current too large mid-work): append new child `TASK-NNNN-*` rows with Why-ref to parent; parent becomes Done only if fully decomposed with no remaining work, otherwise reduce its scope, set its `State` and Sub-Tasks truthfully, and keep `Current:` on the actual next active task.

Commits: 1/TASK on Done (not per Sub-Task), msg `TASK-NNNN: <Name>`. Local only, no auto-push - history stays local until the user explicitly requests a manual push. Never push automatically. Never stack uncommitted work across TASKs.

Runtime task-tracking tool: in-session micro-tracking only; tasks.md+tasks/ = project-persistent truth. No duplication, no confusion.

Deviation: rules strict; only if rule blocks core progress -> Note in Deviations + minimal-invasive alternative + new Sub-Task to reconcile.

Reconc execution: workflow commands and generated hooks resolve `.build/bin/reconc` and root `reconc` first for development/self-hosting without platform probes, then repo-local binaries under `tools/reconc/dist/`: `reconc-darwin-arm64`, `reconc-darwin-amd64`, `reconc-linux-arm64`, `reconc-linux-amd64`, `reconc-windows-amd64.exe`. PATH/global `reconc` is only a final fallback so Codex, Claude Code, OpenCode, Grok Build, git hooks and fresh shells work without external installation.

Session entry for implementation work: read active task context if relevant -> read relevant `docs/spec.md` section -> read `docs/decisions.md` only for rationale/tradeoff questions -> read relevant `research/...` code before implementing referenced features -> reuse existing modules/naming before adding new structure.


## Go File Size Budget

- Target Go file size: 300-700 LOC. 700-1000 LOC is acceptable only when the file still has one clear primary concept.
- More than 1000 LOC requires a split review before adding more logic. More than 1500 LOC is a refactor candidate unless it is generated, append-only schema/migration data, registry, or fixture material.
- New features must not bloat oversized files when a clean concept split exists.
- Keep one runtime purpose per file: adapter, registry, service, renderer, validator, manifest, repository, usecase, or narrowly scoped helper.
- Prefer several small AI-readable files over one large mixed file, but do not create unnecessary abstraction layers just to reduce line count.

## Codebase Hygiene

No parallel variants: no `_v2`, `_new`, `_final`, duplicate component families, or shadow subsystems. Refactor in place after reading callers.

Before adding an abstraction or helper, search for the existing pattern. Extend the established project mechanism whenever possible.

One concept has one owner. If config/schema/policy/API meaning exists in two places, consolidate or link to the canonical source.

Generated artifacts, build outputs, local DBs, and caches do not belong in source paths unless the spec explicitly defines them as committed fixtures.

## File And Path Conventions

Important current paths:
- `docs/` - project docs and technical specs; every docs file must be listed/understood by this AGENTS.md.
- `README.md` - static private GitHub team info page; not architecture/task/doc source-of-truth and not autonomous agent context unless explicitly requested.
- `research/` - read-only source/reference repos.
- `_drop/` - manual intake only; do not inspect or touch without an explicit current-task user command naming `_drop/`.
- `tools/reconc/` - operative tool copy used for coding support; not a research source folder.
- New/empty repo default paths are flat-root: `backend/`, `frontend/`, `db/`, `config/`, `assets/`, `scripts/`, `build/` as needed.
- Existing `codebase/` repos are supported: use `codebase/backend/`, `codebase/frontend/`, `codebase/db/`, `codebase/config/`, `codebase/assets/`, `codebase/scripts/`, `codebase/build/`.
- Future backend code belongs under the selected backend owner path, not random root `cmd/`/`internal/`/`pkg/` unless the existing repo already uses that pattern.
- Future frontend dependency installs must stay in the selected frontend owner path and remain gitignored.

Do not add random root product directories. Move/import external product modules only into the matching selected owner path; operative tools go to `tools/`, read-only references go to `research/`.


## Final Local Checklist

Before finishing repo work:
- Stale old product names checked in touched files.
- Relevant spec section still matches the change.
- Research refs preserved when touched.
- `_drop/` left untouched unless the user explicitly requested work on it.
- `README.md` left unread/untouched unless the user explicitly requested README work.
- No global Codex agent/config file touched.
- Go-specific checks run when Go code changed.
- `git status --short` reviewed.
