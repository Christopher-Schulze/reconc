# TASK 381: Bind require-script execution to validated identity

## Why

`require_script` validates a repository-relative regular file and later executes the pathname. A concurrent replacement between validation and `exec` can run bytes that were never validated.

## Acceptance

- The executed program is cryptographically or descriptor-bound to the exact validated regular-file identity.
- Parent symlinks, leaf symlinks, hard-link swaps, renames, and content replacement fail closed on Unix and Windows.
- Script arguments, working directory, environment, timeout, and cancellation behavior remain compatible.
- Deterministic hooks reproduce replacement at each validation-to-exec boundary; adversarial tests prove replacement bytes never execute.

## Sub-Tasks

- [x] Map the current resolver, script cache, execution, timeout, and platform call chain.
- [x] Design a cross-platform bound execution mechanism without repository self-hosting.
- [x] Add deterministic TOCTOU hooks and adversarial regressions.
- [x] Run focused script and runtime tests on supported local platforms.

## Notes

- Verified from finding 12.
- Existing path containment and cache identity work closes validation-time traversal but `scriptCommand` still executes the lexical name.
- A second `Lstat` alone does not close the final race.
- The execution path now needs a byte snapshot rather than another pathname check: Unix cannot portably execute an arbitrary descriptor on every supported host, and Windows `exec.Cmd.ExtraFiles` is unavailable. A private, extension-preserving snapshot keeps native and shell dispatch compatible while detaching launched bytes from subsequent repository replacement.
- Deterministic stages cover post-path-validation, post-open, post-snapshot, and post-command-construction replacement. Rename, hard-link, leaf-link, parent-link, size-changing content, and same-metadata content attacks all leave both original and replacement markers unexecuted.
- Focused runtime tests and `make test-fast` pass on macOS. Windows-specific execution was intentionally not run locally; the shared adversarial and compatibility tests remain enabled for the Windows CI runner.

## Deviations
