# TASK 553: Reduce agent context and command friction

## Why

The core guide and skill are already bounded, but that alone does not prove efficient multi-turn use. Repeated hook context, duplicate discovery, oversized diagnostics, or ambiguous actions can still waste tokens and cause incorrect agent behavior.

## Acceptance

- Representative agent workflows have reproducible before/after measurements for emitted bytes, command count, context reinjection, and decision correctness.
- The default guide remains within 4,096 bytes and the skill within 5,000 bytes; lazy references remain complete and resolvable after installation.
- Repeated unchanged guidance is not redundantly injected where host delivery semantics permit safe deduplication; first-turn, changed-policy, new-session, and compaction recovery remain correct.
- Typed argv/cwd/authorization/evidence remains sufficient to execute each next action without reconstructing shell commands from prose.
- Read-only review remains non-mutating, and compact output never suppresses blockers, uncertainty, or required evidence.
- CLI/skill/hooks/CI/MCP boundaries and the install/update behavior are understandable through the normal entry flow without loading the full manual.

## Sub-Tasks

- [x] Measure existing guidance and command transcripts for representative agent workflows.
- [x] Remove proven repetition and ambiguity using existing output/section/action owners.
- [x] Verify context lifecycle and add real CLI workflow regressions.
- [x] Update skill and embedded documentation, validate installed references, and record measured effects.

## Technical Plan

1. Inspect `skills/reconc/SKILL.md`, its four references, `internal/agentguide/agentguide.go` and `guide.md`, `internal/tasklifecycle/briefing.go`, session-briefing construction, typed remediation, and hook context owners. Apply the skill-creator instructions when actually editing the skill, not by creating another skill.
2. Capture these workflows from isolated repositories: read-only audit with absent/stale policy, authorized edit blocked by required evidence, command failure and remediation, completed candidate proof, and missing/stale skill update. Record total output bytes, duplicate static context, invocations until the correct action, and required fields used. Report tokenizer-based counts only when measured with an identified tokenizer; bytes are not tokens.
3. Prefer existing agent-intro stable section IDs, session-briefing, next, typed actions, and current compact/JSON modes. Remove redundant prose/platform inventories from default guidance when lazy references or registry-owned queries already supply them. Keep errors actionable and fields stable; no synonym commands or new facade solely to shorten typing.
4. Where repeated prompt hooks inject static guidance, bind deduplication to repository/policy/adapter/guide identity and session/compaction generation. A delivery attempt is not proof of delivery. Advance state only after the host's supported delivery point; where acknowledgement is unavailable, retain the minimal safe reinjection instead of assuming the model remembers.
5. Preserve dynamic blockers, changed source/lock, failed evidence, task/run state, and uncertainty on every relevant decision. New child sessions and context compaction must regain necessary guidance. Never cache a stale allow or omit an authorization requirement to save tokens.
6. Make installation/update instructions match TASKS 551-552 exactly. Explain that the skill is guidance, native hooks enforce supported boundaries, and CI checks the chosen Git candidate. Keep the existing optional downstream MCP gateway documented only for explicitly routed tools; do not add a general MCP server without a demonstrated missing workflow.
7. Revalidate the bundled/installed references after edits. Update `docs/documentation.md` only for changed product usage, and keep the source and embedded guide semantically aligned rather than duplicating the entire reference text.

## Verification

Run `go test ./internal/agentguide ./internal/cli ./internal/tasklifecycle ./internal/runtime/agentsession ./scripts/audits/publication` and the portable skill validation used by TASK 543. Include actual filesystem snapshots for read-only flows and stale-policy handling. Compare unchanged multi-turn and compaction transcripts; require no repeated full static payload in qualified unchanged flows and no extra command to recover the same typed next action. Seek a material reduction in the highest-cost measured flow; if the baseline is already minimal, record the evidence rather than removing necessary content to reach an arbitrary token quota.

## Dependencies

TASKS 546-552 for final host/context/install contracts. Coordinate any hot-path performance changes with TASK 554.

## Notes

The current skill was 3,795 bytes and the default embedded guide 1,650 bytes before this edit, both below budget. Existing read-only and typed-remediation regressions from TASKS 534 and 543 remain required.

An isolated two-rule repository produced 2,406 bytes from full `check --json` and 883 bytes from persisted `next --json` across two invocations. Direct `next --write src/main.go --json` returned the same first blocker and typed evidence action in one invocation and 875 bytes, 2,414 bytes or 73.4% less output. A real failed `go test ./...` did not satisfy `require_command_success`; the next action retained literal shell text, `cwd`, authorization, and required evidence. The new CLI regression checks the first action, all blockers in full `check`, failed and successful command outcomes, and unchanged compiled-policy bytes. Read-only missing/stale briefings were 436/454 bytes and left the isolated repository's file hashes unchanged. The standard fresh briefing was 257 bytes.

After the guidance edit, the skill is 4,057 bytes and the default embedded guide is 1,906 bytes. The first-use skill plus decision path fell from 7,084 to 4,932 bytes (30.4%); after the guide is already in context, the repeated decision path falls from 3,289 to 875 bytes (73.4%). These are byte counts, not tokenizer counts. The default teaching surfaces retain the registered host inventory and exact evidence tokens because the publication contract requires them; no test was weakened to simulate a reduction.

The DSH adapter already registers its static text through `systemPrompt.context`. The [host's documented `PromptContext` contract](https://github.com/deepseek-ai/deepseek-harness/blob/master/docs/subsystems/system-prompt.md) adds a current snapshot only after a change or compaction, not on every unchanged turn (verified 2026-09-13). The existing Codex lifecycle regression proves distinct startup/resume/clear versus compact routes; agent-session tests retain bounded recovery behavior. No local `dsh` executable is available, so DSH deduplication is host-source evidence rather than a live provider execution claim; TASK 555 owns five-host qualification. TASK 552's real CLI tests cover missing and stale skill update actions; this task changes only guidance for their interpretation.

Completion evidence (2026-09-13, macOS arm64): `go test ./internal/agentguide ./internal/cli ./internal/tasklifecycle ./internal/runtime/agentsession ./scripts/audits/publication -count=1` passed. `make test` passed publication auditing, both uncached race modules, and release trust (`release-trust: ok`). `make vet`, `make lint`, `make build`, `make self-host`, and the skill-creator quick validator passed. The embedded guide section catalog and portable skill references remain resolvable. `git diff --check` passed. No product tag or release publication was created. Hosted CI and authenticated five-host qualification belong to TASK 555.

## Deviations

None.
