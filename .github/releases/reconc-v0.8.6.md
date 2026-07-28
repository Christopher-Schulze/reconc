# reconc v0.8.6

`v0.8.6` turns Reconc's repository policy engine into a judge-ready proof loop
for AI-assisted engineering: agents can move quickly, but completion remains a
deterministic repository decision backed by current evidence.

## Proof, completion, and recovery

- `reconc done` now proves the full completion contract instead of accepting a
  merely clean policy check: TASK state, required claims, current command
  evidence, lock identity, Git state, and unresolved decisions are bound into
  one deterministic report.
- `reconc next`, bootstrap diagnostics, hook diagnostics, and run-loop status
  expose one concrete recovery action without hiding ambiguity or drift.

## Agent runtime control

- Repository-scoped `reconc run on|off|reset|status|log` uses bounded,
  per-session continuation state and no-progress guards instead of relying on
  prompt obedience.
- GitHub Copilot joins the existing runtime adapters through its documented
  repository hook contract. Public runtime claims remain capability-specific;
  git pre-commit is the universal hard backstop.
- Codex activation now handles a final `[features]` table without a trailing
  newline, and bootstrap removal converges when an owned file or managed block
  was already removed while still preserving genuine user drift.

## Generic JavaScript and TypeScript assurance

- Stack detection distinguishes JavaScript, TypeScript, npm, pnpm, Yarn, and
  Bun from lockfiles and `packageManager` metadata with explicit ambiguity.
- New npm, pnpm, Yarn, and generic TypeScript packs require evidence only for
  real, non-empty declared scripts. Reconc never installs or invokes a package
  manager and never invents test, lint, build, or typecheck commands.
- Monorepo commands are directory-scoped; malformed manifests become local
  findings; BOM-prefixed manifests work; fixtures and examples are excluded
  without aborting unrelated package checks.

## Public and release surface

- The README, hero, FAQ, command metadata, shell completion, man page, embedded
  agent guide, and deterministic tests share one product contract: **AI agents
  say they're done. Reconc proves it.**
- Publication auditing rejects private-project vocabulary, personal paths,
  secret-shaped values, sensitive filenames, and placeholder residue from both
  the tracked tree and the post-boundary commit history.
- Release artifacts are checksum-bound, carry deterministic SPDX 2.3 and
  CycloneDX 1.6 SBOMs, and receive GitHub build-provenance attestations.

## Install

```sh
curl -fsSL https://raw.githubusercontent.com/Christopher-Schulze/reconc/reconc-v0.8.6/install.sh \
  | RECONC_INSTALL_DIR="$HOME/.local/bin" sh -s -- 0.8.6
"$HOME/.local/bin/reconc" --version
```

Set `RECONC_REQUIRE_ATTESTATION=1` to require `gh attestation verify`. Reconc's
runtime remains one offline Go binary with no Node, Bun, model, daemon, Docker,
or network dependency.

## Release artifacts

- `reconc-0.8.6-darwin-amd64`
- `reconc-0.8.6-darwin-arm64`
- `reconc-0.8.6-linux-amd64`
- `reconc-0.8.6-linux-arm64`
- `reconc-0.8.6-windows-amd64.exe` as the existing compatibility build; new
  Windows installer and launch-proof work remains deferred
- Bash, Zsh, and Fish completions, man page, public schemas, deterministic
  SPDX/CycloneDX SBOMs, and `SHA256SUMS`
