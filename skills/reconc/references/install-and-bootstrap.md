# Install and bootstrap

## Install Or Build

Prefer an installed `reconc` binary:

```bash
reconc --help
```

If the binary is not installed and the current repo is the `reconc` source
tree, build an owned, pruneable session binary:

```bash
mkdir -p .reconc/cache
go build -o .reconc/cache/reconc-session ./cmd/reconc
.reconc/cache/reconc-session install-cli
reconc --version
```

The path-qualified binary is needed only for that one installation call.
`install-cli` atomically publishes the exact running build and proves bare
`reconc` resolves to it. If PATH activation needs a new terminal, apply the
exact emitted remediation before bootstrap. In any other repo, use the
portable binary shipped with its Reconc toolkit for the same one-time command;
never keep navigating versioned artifact paths.

## Bootstrap A Repo

For a new target repo:

```bash
reconc init .
reconc session-briefing . --json
```

`init` is the canonical CLI onboarding path. It scaffolds `.reconc.yml` and
`AGENTS.md` when missing, compiles the lockfile, installs git hooks, and wires
native agent hooks when supported directories such as `.claude/`, `.codex/`,
`.cursor/`, `.opencode/`, `.devin/`, `.agents/`, `.kilo/`, `.omp/`, `.pi/`,
`.zcode/`, or `.grok/`
already exist.
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

For a lighter/manual start:

```bash
reconc init .
reconc refresh .
```

Default new repos should normally use the bundled `default` + `agent` presets.
Only add stronger presets when the repo is ready for them:

- `docs-sync`: public surface changes should update user-facing docs
- `strict`: source edits require tests, architecture reads, and `ci-green`
- `release`: release manifests/artifacts require changelog, checksums, and
  verification
