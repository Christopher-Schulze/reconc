# TASK 254: Make RECONC_HOME override test platform-neutral

## Why

The post-audit native Windows run passed the complete focused preflight but
spent another nineteen minutes before `internal/presets.TestHomeRespectsEnvVar`
rejected Windows' correct native normalization of a hard-coded POSIX path. The
product contract is platform-neutral; the regression and the preflight must be
platform-neutral too.

## Acceptance

- The `RECONC_HOME` override test uses an absolute native path and verifies that
  `Home` preserves that exact platform-valid override.
- The focused Windows preflight includes the regression and `internal/presets`,
  so the same class fails inside the four-minute boundary.
- The focused test, preflight script syntax, formatting, Vet, Staticcheck, root
  race suite, native Windows preflight, full Windows tests, CLI smoke, installer,
  and cross-platform gates pass.

## Sub-Tasks

- [x] Replace the hard-coded POSIX override with an absolute native test path
- [x] Add the regression and presets package to the focused Windows preflight
- [x] Run local focused and repository verification
- [~] Prove the preflight and full suite on native Windows
- [ ] Archive the TASK and preserve exact CI evidence

## Notes

- CI run `32551738459` at source `e6dfa61b` passed CodeQL, Linux, macOS,
  LangChain, release trust, and the focused Windows preflight. The full Windows
  suite failed only `internal/presets.TestHomeRespectsEnvVar`: it expected
  `/custom/path`, while the runner correctly resolved `D:\custom\path`.
- The focused regression, the expanded preflight, Bash syntax, full root race
  suite, Vet, and Staticcheck pass locally. Production code is unchanged.

## Deviations

None.
