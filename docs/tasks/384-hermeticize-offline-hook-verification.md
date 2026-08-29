# TASK 384: Hermeticize offline hook verification

## Why

Offline hook verification invokes Git with the ambient `GIT_*` environment, passes the complete parent environment into its child process, and constructs its overlay `PATH` by appending the original value even when it is empty. Host control variables, credentials, configuration, and an implicit current-directory PATH entry can therefore enter or alter a supposedly disposable verification.

## Acceptance

- Every Git subprocess uses the existing hermetic `gitexec.CommandContext` contract.
- An absent or empty `PATH` never creates an empty path element.
- Verification uses an explicit minimal environment allowlist, preserving only variables required by the disposable binaries and verified host contracts.
- Adversarial tests poison Git environment variables, Git config, PATH, and current-directory executables and prove isolation.

## Sub-Tasks

- [ ] Inventory all offline verification subprocess signatures and environment construction.
- [ ] Route Git through `gitexec` and build PATH without empty entries.
- [ ] Add poisoned-environment regressions.
- [ ] Run focused hook verification tests.

## Notes

- Verified from findings 23 and 24 plus worker findings 716 and 843.
- `internal/gitexec.CommandContext(ctx, directory, objectDirectories, args...) *exec.Cmd` already strips Git environment and applies hermetic config.
- `newHookVerificationWorkspace` currently overlays a few keys onto `os.Environ`, so unrelated credentials and process-control variables remain available to every verification child.

## Deviations
