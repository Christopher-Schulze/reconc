# TASK 301: Sanitize every proof and agent-session Git environment

## Why

Command-proof and agent-session Git subprocesses inherit ambient `GIT_DIR`, `GIT_WORK_TREE`, `GIT_INDEX_FILE`, and config variables. `-C` and `cmd.Dir` do not override all of them, so evidence can be captured from foreign Git state.

## Acceptance

- Every proof, completion, Stop, memory-path, and shell-alias Git command uses one shared hermetic environment contract.
- Ambient repository, index, object, config, hook, prompt, locale, and optional-lock variables cannot redirect or mutate inspection.
- Commands retain only explicitly required safe overrides and preserve exact output bounds and timeouts.
- Malicious-environment tests prove foreign HEAD, index, aliases, and common directories cannot enter evidence.

## Sub-Tasks

- [x] Inventory all Git subprocess owners outside repository sync
- [x] Extract a shared hermetic inspection environment
- [x] Migrate commandproof and agent-session callers
- [x] Add hostile-environment and cross-platform tests

## Notes

- Evidence: `internal/commandproof/proof.go:389,433`, `internal/runtime/agentsession/stop_git.go:25-33`, `shell_guard.go:277-305`, and `memory_paths.go:133-145`. `internal/bootstrap/repository_sync_git.go:120-158` is the reference.
- `internal/gitexec` now owns Git argv and environment construction. It removes
  every ambient `GIT_*` variable, pins global/system config, prompts, optional
  locks, pager, hooks, fsmonitor, untracked cache, and locale, and permits only
  Repository Sync's typed ephemeral object-directory override.
- Command Proof, Runtime Git diffs, agent-session Stop/completion Git reads,
  memory common-directory resolution, Git alias inspection, and Repository Sync
  now use that one contract. The remaining production Git owners are hook-path
  diagnosis, offline hook fixture construction, and CLI workflow status; they
  do not contribute proof or agent-session evidence and retain their distinct
  semantics.
- Real-repository hostile-environment regressions cover foreign HEAD, logical
  index, object database, common directory, process-memory config, global
  aliases, repository aliases, prompt, locale, and optional-lock routing.
- Verified with focused hostile-environment and consumer tests, Windows amd64
  test compilation and vet, pinned Staticcheck `v0.8.1`, `make vet`,
  `make test`, and `make self-host`.

## Deviations

None.
