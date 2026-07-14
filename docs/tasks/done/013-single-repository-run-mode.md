# TASK 013: Single repository run mode

## Why

The legacy prompt-scoped `/runloop` and session mode duplicate repository run control, couple durable autonomy to fragile message parsing, and can disable an explicitly enabled repository run after ordinary prompts or interrupts. Reconc needs one deterministic autonomous mode controlled only through `reconc run on|off`, with TASK exhaustion providing the automatic terminal shutdown.

## Acceptance

- `reconc run on|off|status|log` is the only run-control surface.
- Prompt text, user interrupts, session start/end, runtime changes, and application restarts never enable or disable repository run mode.
- Enabled run mode continues every executable TASK disposition across all registered runtimes and automatically disables when no executable TASK work remains.
- Explicit `reconc run off` is the only manual disable action.
- Legacy session mode, `/runloop` parsing, compatibility CLI aliases, prompts, state branches, tests, generated guidance, and audit requirements are removed.
- Run state, Stop handling, no-progress protection, checkpoints, policy gates, retention, and hook adapters remain deterministic, bounded, fail-closed, and efficient.
- Documentation, scaffold output, tests, race tests, vet, static analysis, release trust, self-hosting, hook probes, and benchmarks pass on the final state.

## Sub-Tasks

- [x] Inventory the complete legacy surface and lock the single-mode contract.
- [x] Remove prompt/session runloop behavior and compatibility interfaces.
- [x] Harden durable repository run transitions and terminal auto-disable.
- [x] Propagate hooks, bootstrap, scaffold, audits, retention, and documentation.
- [x] Run fresh-eyes verification, archive the TASK, and commit the complete change.

## Notes

- GOLEM remains read-only evidence; all implementation stays inside standalone Reconc.
- Ordinary user messages and explicit runtime interrupts stop only the current agent invocation. They never mutate durable repository run state.
- Historical archived TASK files remain immutable records and are not rewritten to describe the new target state.

## Deviations
