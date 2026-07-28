# Reconc Harness Template

This folder is the repo-agnostic Reconc workflow template.

Do not use this README as the rollout guide. The authoritative rollout procedure is `BOOTSTRAP.md`.

The supported user path is:

```sh
reconc init . --profile advanced --no-hooks --json
```

That command materializes this exact template from the immutable pack embedded
in the installed CLI. It records the pack version and digest in the bootstrap
plan and portable `.reconc/install.lock.json` receipt. Do not clone or copy the
standalone repository for a normal rollout. After updating the global CLI, use
`reconc repo sync plan|apply|verify` to upgrade receipt-owned pack bytes.

Contents:

- `audits/` - template harness audits and tests.
- `config/workflow/` - task schema, prune policy, claim bindings and stack config.
- `utils/` - repository run, TASK promotion, claim and prune utilities.
- `repo-root-scaffold/` - root files/excerpts copied or merged into a target repo by the bootstrap agent.

Hook artifacts inside `repo-root-scaffold/` are generated from the Reconc hook generator. Refresh them with `reconc hook sync-scaffold tools/reconc/harness/template/repo-root-scaffold`; never hand-edit or copy them from a source-specific harness.

Standalone contributors edit this canonical source tree, then run
`go run ./scripts/build/harness-pack --write` and
`go run ./scripts/build/harness-pack --check`. The generated manifest and
archive must change together; generated coverage reports never enter the pack.

After rollout, run `reconc hook status . --json`. A `configured` artifact is
statically complete and discoverable, not automatically loaded, observed, or
enforced. Verify each required host surface and route independently; do not
reuse Cursor IDE evidence for CLI/cloud or Kilo CLI evidence for its VS Code
host.
