# TASK 381: Bind require-script execution to validated identity

## Why

`require_script` validates a repository-relative regular file and later executes the pathname. A concurrent replacement between validation and `exec` can run bytes that were never validated.

## Acceptance

- The executed program is cryptographically or descriptor-bound to the exact validated regular-file identity.
- Parent symlinks, leaf symlinks, hard-link swaps, renames, and content replacement fail closed on Unix and Windows.
- Script arguments, working directory, environment, timeout, and cancellation behavior remain compatible.
- Deterministic hooks reproduce replacement at each validation-to-exec boundary; adversarial tests prove replacement bytes never execute.

## Sub-Tasks

- [ ] Map the current resolver, script cache, execution, timeout, and platform call chain.
- [ ] Design a cross-platform bound execution mechanism without repository self-hosting.
- [ ] Add deterministic TOCTOU hooks and adversarial regressions.
- [ ] Run focused script and runtime tests on supported local platforms.

## Notes

- Verified from finding 12.
- Existing path containment and cache identity work closes validation-time traversal but `scriptCommand` still executes the lexical name.
- A second `Lstat` alone does not close the final race.

## Deviations
