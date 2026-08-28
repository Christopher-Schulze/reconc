# TASK 301: Sanitize every proof and agent-session Git environment

## Why

Command-proof and agent-session Git subprocesses inherit ambient `GIT_DIR`, `GIT_WORK_TREE`, `GIT_INDEX_FILE`, and config variables. `-C` and `cmd.Dir` do not override all of them, so evidence can be captured from foreign Git state.

## Acceptance

- Every proof, completion, Stop, memory-path, and shell-alias Git command uses one shared hermetic environment contract.
- Ambient repository, index, object, config, hook, prompt, locale, and optional-lock variables cannot redirect or mutate inspection.
- Commands retain only explicitly required safe overrides and preserve exact output bounds and timeouts.
- Malicious-environment tests prove foreign HEAD, index, aliases, and common directories cannot enter evidence.

## Sub-Tasks

- [ ] Inventory all Git subprocess owners outside repository sync
- [ ] Extract a shared hermetic inspection environment
- [ ] Migrate commandproof and agent-session callers
- [ ] Add hostile-environment and cross-platform tests

## Notes

- Evidence: `internal/commandproof/proof.go:389,433`, `internal/runtime/agentsession/stop_git.go:25-33`, `shell_guard.go:277-305`, and `memory_paths.go:133-145`. `internal/bootstrap/repository_sync_git.go:120-158` is the reference.

## Deviations

None.
