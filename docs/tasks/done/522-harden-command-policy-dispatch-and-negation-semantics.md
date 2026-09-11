# TASK 522: Harden command-policy dispatch and negation semantics

## Why

Static command enforcement currently recognizes a substantial set of shell
wrappers, but several common dispatchers can still hide the effective executable
from `forbid_command`. Separately, the pre-command trigger prefilter treats every
composite containing `forbid_command` as positive: it evaluates the composite
only when an inner forbidden-command matcher hits. That is correct for the
existing positive `any_of` path, but it makes `not { forbid_command }`
ineffective before execution because a non-match is skipped and a match is then
accepted by logical negation.

This task consolidates audit candidates C217 and C245. It must improve the
cooperative-agent enforcement boundary without claiming that static parsing is
a sandbox or protection against a hostile same-user process.

## Acceptance

- Classify the effective executable behind common dispatchers including
  `taskset`, `bwrap`, `unshare`, `nsenter`, `pkexec`, `busybox`, `systemd-run`,
  and GNU `parallel` whenever their argument grammar is statically resolvable.
- For every supported dispatcher, parse option arguments, `--` termination,
  assignments, nested wrappers, absolute executable paths, and quoted static
  values without treating option operands as commands.
- Return an explicit incomplete-analysis result for dynamic operands,
  unsupported option shapes, command strings evaluated by another shell, or
  dispatcher modes whose executable cannot be identified before execution.
- Preserve fail-closed behavior at every blocking command-policy caller when
  command analysis is incomplete; warning and reporting modes must retain their
  current diagnostic semantics.
- Evaluate configured Git aliases through the existing bounded alias snapshot,
  or classify aliases that cannot be expanded safely as incomplete. Never read
  mutable alias configuration outside the identity already bound to the
  pre-decision operation.
- Compile or derive pre-command composite triggers with logical polarity so
  `not { forbid_command }` evaluates on the commands for which its inner check
  passes, while positive `forbid_command`, `all_of`, and `any_of` retain their
  documented truth semantics.
- Define behavior for nested `not`, mixed command/non-command checks, and
  multiple `forbid_command` checks. If a shape cannot be evaluated safely in
  the pre-command phase, reject it during policy authoring and lockfile
  admission with one precise error instead of silently weakening enforcement.
- Preserve full completion-phase composite semantics and the existing
  pre-write phase separation for future evidence, scripts, claims, and files.
- Add table-driven parser and evaluator regressions for every dispatcher,
  positive and negative matches, nested wrappers, unresolved input, alias
  expansion, prefix collisions, and nested composite polarity.
- Keep command analysis bounded in input bytes, recursion depth, invocation
  count, alias expansion, and total work. Existing hot-path benchmarks must not
  regress materially without measured justification.
- Update command-policy and threat-model documentation to state the exact
  supported static boundary and the behavior of incomplete analysis.
- Pass focused shell-command and runtime tests under the race detector, then
  the complete required test, vet, lint, coverage, and self-host gates.

## Sub-Tasks

- [x] Read the complete shell-command parser, dispatcher helpers, alias snapshot path, command-policy callers, composite compiler/indexes, and phase-validation contracts; record the exact current grammar and invariants.
- [x] Design one bounded dispatcher-resolution contract that reuses existing parsing primitives and represents resolved, non-command, and incomplete outcomes without parallel classifiers.
- [x] Implement and test the named dispatcher grammars and bounded Git-alias handling, preserving every existing wrapper and command-match behavior.
- [x] Make pre-command composite trigger analysis polarity-aware, or reject only the precisely unsupported policy shapes at both authoring and compiled-lock boundaries.
- [x] Add deterministic unit, integration, adversarial, and regression coverage for wrapper chains, options, quoting, aliases, nested composites, and incomplete analysis.
- [x] Measure the affected command hot path, review allocations and bounds, and remove only proven regressions without weakening identity or fail-closed behavior.
- [x] Propagate the final supported-command and composite-phase contract into user and architecture documentation.
- [x] Run focused race tests and all required repository gates; re-read every changed file and archive the TASK only after all Acceptance items are proven.

## Notes

### Observed behavior

- `internal/shellcommand/shellcommand.go` already unwraps `command`, `builtin`,
  `exec`, `env`, `sudo`, `doas`, `nohup`, `rtk`, `nice`, `timeout`, `setsid`,
  `stdbuf`, `time`, and `chroot`. New wrappers extend that loop. `find`,
  `xargs`, `flock`, and `watch` stay launchers; GNU `parallel` joins them.
- The pre-command skip in `compositeRuleTriggerMatches` was polarity-blind.
  `not { forbid_command }` is now reached from the parent path trigger.
  Positive `all_of`/`any_of` still require a forbid matcher hit.
- Nested composites remain authoring- and lock-rejected. Incomplete inner
  forbid analysis is a `checkEvalError` so `not` fails closed instead of
  treating an unknown command as a forbid hit.

### Implementation

- New dispatcher option grammars live in `internal/shellcommand/dispatchers.go`
  and reuse `skipDispatcherOptions` plus known-option classifiers. Unknown
  options and missing operands are incomplete.
- `skipNoArgumentOptions` no longer allocates a map per `setsid` peel.
- Git aliases stay on the existing bounded snapshot path; destructive-guard
  tests cover the new wrappers.

## Deviations

None.
