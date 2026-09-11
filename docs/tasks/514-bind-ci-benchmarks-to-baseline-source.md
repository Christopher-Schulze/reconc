# TASK 514: Bind CI benchmarks to the baseline source on the same runner

## Why

Run 34590556975 successfully records the complete benchmark suite, all bounded
profiles, and retained artifacts on commit 3aef4ab510440978086b818fff145714d735fbd4.
Comparison correctly rejects the checked baseline's physical `Apple M1` against
the runner's `Apple M1 (Virtual)`. The workflow needs a measurement of the same
baseline source on its own runner, without changing the reviewed baseline or
weakening hardware identity and regression limits.

## Acceptance

- CI resolves the exact clean source commit from the validated checked baseline
  and measures that commit and the candidate on the same runner/toolchain.
- A generated runner baseline preserves every checked tolerance and accepts
  only clean measurements of the identical baseline commit and parameters.
- The checked baseline remains byte-identical; incompatible environments and
  real regressions still fail comparison, including performance recipe use.
- CI retains baseline/candidate measurements, the generated baseline, comparison,
  and all existing bounded profile artifacts within its explicit job timeout.
- A native workflow run proves source selection, recording, comparison, and
  artifact retention. Any actual regression is diagnosed rather than hidden.

## Sub-Tasks

- [ ] Add strict baseline-commit inspection and source-bound runner baseline generation to the existing Go benchmark tool.
- [ ] Wire the workflow to measure the checked source and candidate on the same runner, retaining reviewed tolerances and artifacts.
- [ ] Cover identity, cleanliness, parameter, tolerance, and workflow boundaries; update the existing performance documentation.
- [ ] Run local and native gates, review actual comparison/profile evidence, archive, commit, and push.

## Notes

- Source: https://github.com/Christopher-Schulze/reconc/actions/runs/34590556975.
  Recording/profiling and upload pass; comparison fails solely on CPU identity.
- Reuse `readBaseline`, `readResult`, `validateBaseline`, and the existing
  `baseline --refresh` command. Add an optional reference baseline for generated
  runner artifacts so the original tolerance policy is preserved exactly.
- Keep the physical baseline, existing compatibility checks, and existing
  default baseline-refresh behavior unchanged. Do not strip the virtual CPU
  suffix, choose a newer baseline commit, or normalize away a real regression.
- Keep this as Go/Bash tooling with the existing pinned checkout action; no
  new runtime dependency, branch, product version, tag, or release is required.

## Deviations

- Native workflow proof requires a candidate commit on `main`. Retain this TASK
  as active while publishing locally verified code under the standing push
  instruction; archive only after the native run proves acceptance.
