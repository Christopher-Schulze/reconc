# TASK 384: Hermeticize offline hook verification

## Why

Offline hook verification invokes Git with the ambient `GIT_*` environment, passes the complete parent environment into its child process, and constructs its overlay `PATH` by appending the original value even when it is empty. Host control variables, credentials, configuration, and an implicit current-directory PATH entry can therefore enter or alter a supposedly disposable verification.

## Acceptance

- Every Git subprocess uses the existing hermetic `gitexec.CommandContext` contract.
- An absent or empty `PATH` never creates an empty path element.
- Verification uses an explicit minimal environment allowlist, preserving only variables required by the disposable binaries and verified host contracts.
- Adversarial tests poison Git environment variables, Git config, PATH, and current-directory executables and prove isolation.

## Sub-Tasks

- [x] Inventory all offline verification subprocess signatures and environment construction.
- [x] Route Git through `gitexec` and build PATH without empty entries.
- [x] Add poisoned-environment regressions.
- [x] Run focused hook verification tests.

## Notes

- Verified from findings 23 and 24 plus worker findings 716 and 843.
- `internal/gitexec.CommandContext(ctx, directory, objectDirectories, args...) *exec.Cmd` already strips Git environment and applies hermetic config.
- The finding was reconfirmed: `newHookVerificationWorkspace` overlaid only a few keys onto `os.Environ`, leaving unrelated credentials and process-control variables available to every verification child.
- Whole-repository source and Graphify caller searches found two direct Git subprocesses in offline repository initialization and one shared workspace/environment constructor used by offline and live setup.
- Disposable Git initialization and staging now use `gitexec.CommandContext` with the repository as `Cmd.Dir`; no verification-owned direct Git process remains.
- The child environment is built from a sorted explicit map. Disposable home, XDG, temporary, Reconc, Kimi, and Pi roots replace host locations; beyond sanitized PATH, only Windows process-bootstrap variables are inherited where required.
- PATH keeps the disposable Reconc directory first, drops empty, relative, and duplicate entries, and retains absolute host tool directories for Git, POSIX shell, Bun, and approved live-host discovery.
- Adversarial tests prove ambient Git repository/config/index/template controls cannot redirect initialization, unrelated credentials and runtime controls do not cross the child boundary, and current-directory executables are not PATH-resolvable.
- Focused tests passed: `go test ./internal/cli -run 'Test(HookVerif|HookVerify|InitializeHookVerificationRepo|LiveHookVerify|ApplyLiveHookProbe|ReadLiveHookProbe|HookRuntimeTiming)' -count=1`.
- Repository fast gate passed: `make test-fast`.
- Full race, release-trust, vet, and lint gates remain reserved for the final queue-wide verification as requested.

## Deviations
