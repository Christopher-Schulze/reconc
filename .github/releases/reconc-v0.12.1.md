# reconc v0.12.1

Reconc 0.12.1 stops a command-less `env` from blocking later staged policy
gates, keeps macOS tests manual, and lets the operator explicitly waive the
long release gates for one release run.

## Command analysis

- An `env` that ends a wrapper chain without a command only prints the
  environment and is analyzed as its own complete invocation. A recorded bare
  `env` no longer counts as a forbidden command in pre-command or completion
  evaluation, including `reconc ci --staged`.
- Wrappers that start a shell when called without a command (`unshare`,
  `chroot`, `nsenter`, `pkexec`, `sudo -i`) and every other command-less
  wrapper still fail closed.

## CI and release

- Push and pull-request CI run the Linux and boundary checks. macOS tests and
  self-hosting run only when explicitly selected in manual CI; Release neither
  starts nor requires them.
- The Release workflow accepts two operator inputs that are off by default:
  `skip_heavy_gates` waives the race, Vet, Staticcheck and Govulncheck gates,
  and `skip_green_checks` waives the green CI and CodeQL requirement for the
  tagged commit. The release trust gate, the tag and source verification and
  the legacy hook generator check always run.

## Maintenance

- Bootstrap, CLI and hook tests isolate the user-global Kimi Code home, so a
  developer's own Kimi Code installation no longer changes their results.
