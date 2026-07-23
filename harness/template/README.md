# Reconc Harness Template

This folder is the repo-agnostic Reconc workflow template.

Do not use this README as the rollout guide. The authoritative rollout procedure is `BOOTSTRAP.md`.

Contents:

- `audits/` - template harness audits and tests.
- `config/workflow/` - task schema, prune policy, claim bindings and stack config.
- `utils/` - repository run, TASK promotion, claim and prune utilities.
- `repo-root-scaffold/` - root files/excerpts copied or merged into a target repo by the bootstrap agent.

Hook artifacts inside `repo-root-scaffold/` are generated from the Reconc hook generator. Refresh them with `reconc hook sync-scaffold tools/reconc/harness/template/repo-root-scaffold`; never hand-edit or copy them from a source-specific harness.

After rollout, run `reconc hook status . --json`. A `configured` artifact is
statically complete and discoverable, not automatically loaded, observed, or
enforced. Verify each required host surface and route independently; do not
reuse Cursor IDE evidence for CLI/cloud or Kilo CLI evidence for its VS Code
host.
