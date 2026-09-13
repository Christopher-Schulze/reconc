# Install and bootstrap

## Install Or Build

Prefer an installed `reconc` binary:

```bash
reconc --help
```

If the binary is unavailable and a source build is requested, build from the
`reconc` product source root using its verified development target:

```bash
make build
.build/bin/reconc --version
```

Use `.build/bin/reconc install-cli` only when installation is authorized.
Do not initialize Reconc policy, create `.reconc` build state, or install
repository hooks in the product source tree. Product integration tests use
`make self-host` with disposable repositories. Switch to the intended consumer
repository before following bootstrap or the repository decision loop.

`install-cli` atomically publishes the exact running build and the embedded
portable skill at `~/.agents/skills/reconc`, then proves bare `reconc` resolves
to the build. Use `--no-skill` for a binary-only install, `--skill-dir PATH` for
an explicit skill destination, or `--skill-only` for an already owned current
binary. An archive or raw binary download alone does not install a skill.
`doctor --global` verifies receipt-owned skill files but cannot prove that a
host loaded them. If PATH activation needs a new terminal, apply the
exact emitted remediation before bootstrap. For authorized installation from
another repo, use its toolkit's portable binary for that one-time command.
After successful installation, use bare `reconc` instead of versioned paths.

Use `reconc update check --json` to inspect the selected binary and skill
without mutation. Bare `reconc update` updates a stale receipt-owned skill
automatically, including when the binary is already current. If the skill is
absent, follow the explicit `install-cli --skill-only` action in the report;
`update` does not install it silently. Modified or unmanaged skill directories
must be inspected before retrying. `reconc skill-manifest --json` prints the
running binary's embedded skill identity for read-only verification.

## Bootstrap A Repo

For a new target repo:

```bash
reconc init .
reconc session-briefing . --json
```

`init` is the canonical CLI onboarding path. It scaffolds `.reconc.yml` and
`AGENTS.md` when missing, compiles the lockfile, installs git hooks, and wires
native agent hooks when supported directories such as `.claude/`, `.codex/`,
`.cursor/`, `.opencode/`, `.devin/`, `.agents/`, `.kilo/`, `.omp/`, `.dsh/`, `.pi/`,
`.zcode/`, or `.grok/`
already exist.
Load the DeepSeek Harness overlay from the repository root
with `npx --yes @deepseek-ai/dsh@0.1.5-rc.2 --profile headless --patch .dsh/reconc.patch.yml`;
a global `dsh` installation is not required.
Keep DSH customization in a separate overlay. Reinstall, scaffold refresh,
repository sync, and uninstall preserve a modified `.dsh/reconc.patch.yml` and
refuse the operation, including forced reinstall. Preserve edits in the custom
overlay before restoring the exact generated patch and retrying.
Kimi Code is intentionally excluded because its hooks are user-global. Only an
explicit operator action installs them:

```bash
reconc hook install kimi-code
```

Never add a repository argument, install Kimi itself, or write the real Kimi
configuration during tests. Use an isolated temporary `KIMI_CODE_HOME`.
Init mutation performs the same exact running-build installation, fails
before repository writes when bare `reconc` still does not resolve to it, and
transactional verification repeats that check.

For the full repo-local governance rollout with copied Reconc toolkit, harness,
root scaffold, `start.md`, TASK files, and repo-local release binaries, have an
agent follow `harness/template/BOOTSTRAP.md` from the copied toolkit instead of
assuming canonical init copies a complete toolkit.
The advanced pack's `tools/reconc/harness/template/` remains the immutable,
receipt-owned source; the runbook copies it to the project-specific harness
path and never renames or overwrites that source.

Default new repos should normally use the bundled `default` + `agent` presets.
Only add stronger presets when the repo is ready for them:

- `docs-sync`: public surface changes should update user-facing docs
- `strict`: source edits require tests, architecture reads, and `ci-green`
- `release`: release manifests/artifacts require changelog, checksums, and
  verification
