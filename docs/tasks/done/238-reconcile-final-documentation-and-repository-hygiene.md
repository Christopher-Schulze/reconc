# TASK 238: Reconcile final documentation and repository hygiene

## Why

The final candidate audit found one documented harness surface that is present
locally but absent from Git and the deterministic advanced pack: the scaffold
`.vscode/settings.json` file is hidden by the repository-wide editor ignore.
The checkout also retains obsolete ignored analysis scratch and generated
build, release, test-binary, and runtime-lock artifacts. The source candidate
needs one exact documentation, test, script, artifact, Git, and remote-truth
closure before publication work is considered separately.

## Acceptance

- Every documented direct-copy scaffold surface is tracked and included in the
  deterministic advanced harness manifest and archive.
- A regression test prevents the local indexing and watcher load-shed surfaces
  from silently disappearing from the scaffold again.
- The v0.9.7 candidate notes state the Go 1.27 toolchain and the verified
  strict-decoder, canonical-encoding, and concurrency-test improvements.
- Obsolete ignored analysis scratch and disposable build, release, test-binary,
  and repository-runtime artifacts are removed without deleting local TASK
  history or source assets.
- Shell syntax, formatting, module tidiness, publication and harness-pack
  audits, fuzz targets, race tests, Vet, Staticcheck, and repository status are
  verified before the final commit and push.
- `main`, `origin/main`, and the clean local worktree agree after the push. No
  tag, release workflow, or release publication is created.

## Sub-Tasks

- [x] Reconcile the documented scaffold inventory with Git and harness outputs
- [x] Update candidate documentation for the completed Go 1.27 work
- [x] Remove only proven disposable local artifacts
- [x] Run documentation, script, fuzz, race, and static verification
- [x] Re-read all modified files, archive the TASK, commit, and push
- [x] Verify remote and clean-worktree truth

## Notes

- `harness/template/BOOTSTRAP.md` already documented `.vscode/settings.json` as
  a direct-copy local watcher/search control, while the previous `.gitignore`
  hid it and the previous committed pack manifest therefore omitted it.
- `docs/todo/` is ignored, untracked analysis scratch. Its index links to files
  that do not exist, while the nine surviving findings are obsolete, already
  implemented under TASKs 182-214 and later refinements, or based on an invalid
  duplicate-work premise.
- The 157 ignored TASK detail files from TASKs 001-159 are retained as local
  project history. They are not generated build artifacts and are outside the
  cleanup allowlist.
- The scaffold settings file is now an explicit `.gitignore` exception and a
  canonical pack member. The regenerated pack contains 117 files, 832,521
  source bytes, and digest
  `5e1787889b6677294543648a4d78bc143afa82e53b20ffb152208a18168a4882`.
- Obsolete `docs/todo/`, `.build/`, `dist/`, `.reconc/`, and the standalone
  `actioninspect.test` binary were moved to the macOS Trash. The cleanup is
  recoverable until Trash is emptied and did not touch TASK history or source.
- Verification passed: root and portable-harness module tidy diffs, Go and
  shell formatting/syntax, executable script modes, publication audit,
  deterministic harness-pack generation, complete `make test` race and
  release-trust gates, Vet, Staticcheck v0.8.0, all 58 discovered root fuzz
  targets, and the isolated self-hosting bootstrap proof.
- The remote preflight after `git fetch origin` reported zero commits behind
  and 67 commits ahead before this TASK commit. The final push and clean
  local/remote identity checks complete the same closure workflow.

## Deviations

None.
