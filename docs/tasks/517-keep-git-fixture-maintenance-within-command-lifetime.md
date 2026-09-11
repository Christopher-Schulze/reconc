# TASK 517: Keep Git fixture maintenance within command lifetime

## Why

Native macOS run 34594464390 passes the corrected cancellation regression but
fails cleanup of `TestCollectGitWritePathsRenamePreservesSpecialNames`: its
`.git` directory becomes nonempty while TempDir removes it. Both actual rename
assertions complete. The fixture waits for Git commands but permits default
detached automatic maintenance to outlive them.

## Acceptance

- Keep automatic Git maintenance in the foreground for fixture commands so
  their awaited lifetime includes any housekeeping they start.
- Preserve real Git commits, staged/range rename assertions, production command
  behavior, and test cleanup error reporting; do not add sleeps or ignore errors.
- Repeated local Git integration tests and native macOS verification pass.

## Sub-Tasks

- [x] Inspect the failed cleanup and Git command ownership; verify Git's documented maintenance configuration.
- [x] Bind fixture housekeeping to the existing awaited Git command.
- [~] Run local and native verification, archive, commit, and push.

## Notes

- All rename integration cases pass 50 race-enabled repetitions (134.741
  seconds). Complete `go test -p=2 ./...`, `make vet`, and `make lint` pass.
- Run 34594464390 completes the full Windows suite, Linux gates, release trust,
  and LangChain successfully; its only failure is the macOS Git cleanup above.
- Source: https://github.com/Christopher-Schulze/reconc/actions/runs/34594464390.
- Git documents `maintenance.autoDetach` as defaulting to true, with
  `gc.autoDetach` as the compatibility fallback:
  https://git-scm.com/docs/git-config#Documentation/git-config.txt-maintenanceautoDetach.
- The runner uses Git 2.55.0; local Git is Apple Git 2.39.5. Configure both
  foreground controls without disabling maintenance or changing global config.
- The cleanup diagnostic does not identify the concurrent writer. Detached Git
  maintenance is a documented ownership gap in this fixture, not a captured
  process attribution for the failed runner.
- TASK 516 candidate 4a95852b7de67580fe4932e9969fda8f1e4601c3 is committed
  and pushed; its source cancellation regression no longer fails on macOS.

## Deviations

- Native proof requires a locally verified candidate commit on main under the
  standing push instruction. Keep the TASK active until that proof completes.
