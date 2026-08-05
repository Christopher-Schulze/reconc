# Reconc Template Bootstrap

This file is the authoritative rollout runbook for a fresh agent that installs this Reconc workflow package into another repository.

Read this file completely before touching files. The goal is not to invent a new workflow. The goal is to install this portable Reconc/Policy/Hook/Start/TASK governance package into the target repository using `project` placeholders and `stack-config.yaml`.

## Non-Negotiable Contract

- Work only under the target repository. Do not edit global agent files.
- Copy files from this template. Do not move anything out of the source template.
- Do not overwrite existing target-repo files. Merge excerpts surgically.
- Default for new/empty repositories is flat-root: no `codebase/`.
- Existing repositories win. If the repo is mature, analyze it and adapt Reconc to the repo instead of reshaping the repo.
- `tools/reconc/harness/template/` is the immutable source template installed by the advanced CLI profile.
- In the target repo, rename `tools/reconc/harness/template/` to `tools/reconc/harness/<project-name>/`, where `<project-name>` is the target repo directory name normalized to lowercase/kebab-case unless the user explicitly chooses another project name.
- Placeholder is exactly `project` / `Project` / `PROJECT`. No other project placeholder is valid.
- `AGENTS.md` is an excerpt merge: insert the workflow excerpt into an existing `AGENTS.md`; create a new one only when none exists.
- `.gitignore.excerpt` is an excerpt merge after git initialization; do not overwrite `.gitignore`.
- Do not scaffold source-project-specific surfaces into generic repos: no secondary/internal-only binary, no Bun frontend package unless the stack requires frontend, no SQLite initial migration unless durable store is selected, no generated_reference artifacts unless generated references are selected, no `go.mod` unless the target stack/repo is Go.
- Never treat ignored `docs/todo*` or changelog scratch as TASK truth or rollout input. Current TASK truth comes from the target repository's adopted `docs/tasks.md` control plane.
- Template audits are dual-path compatible: they understand both flat-root (`backend/`, `scripts/`, `config/`) and `codebase/` layout.
- Source-specific harness folders are not part of a target rollout. The installed public pack carries only the template harness; the target repo may additionally carry its renamed project harness.
- Hook artifacts are generated artifacts, not hand-maintained source. The canonical source is `reconc hook generate`; before copying hook files from `repo-root-scaffold/`, sync that scaffold with `reconc hook sync-scaffold tools/reconc/harness/<project-name>/repo-root-scaffold`.
- Prefer canonical `reconc init` for every universal surface it owns. Use `reconc bootstrap inspect|plan|apply|verify|remove` only when a separately reviewed lower-level plan is required. Use the manual sections in this runbook for project-specific harness, stack, architecture, and merge decisions that the universal CLI intentionally cannot infer.
- Treat global CLI update and repository sync as separate transactions. After updating the user CLI, use `reconc repo sync plan|apply|verify`; never copy new harness or hook bytes over receipt-owned targets manually.
- A completed rollout must leave the exact bootstrap build directly callable as bare `reconc`. Repo-local hook resolution remains independent and continues to prefer the repository wrapper and binary.

## Source Package

After `reconc init --profile advanced`, the target repository contains:

- `tools/reconc/harness/template/` - generalized harness logic.
- `tools/reconc/harness/template/config/workflow/stack-config.yaml` - stack/layout contract used by template audits.
- `tools/reconc/harness/template/repo-root-scaffold/` - files that are copied or merged into the target repo root.
- `tools/reconc/harness/template/repo-root-scaffold/AGENTS.md` - workflow excerpt, not necessarily the whole target AGENTS file.
- `tools/reconc/harness/template/repo-root-scaffold/start.md` - onboarding entrypoint.
- `tools/reconc/harness/template/repo-root-scaffold/.reconc.yml` - Reconc rules wired to `tools/reconc/harness/project/...`.
- `tools/reconc/harness/template/repo-root-scaffold/.codex/`, `.github/`, `.cursor/`, `.agents/`, `.claude/`, `.opencode/`, `.devin/`, `.kilo/`, `.omp/`, `.pi/`, `.grok/`, `.githooks/` - generated local hook/plugin configs and source-controlled git hook twin.
- `tools/reconc/harness/template/repo-root-scaffold/.cursorindexingignore`, `.codeiumignore`, `.windsurfignore`, `.ignore`, `.vscode/settings.json` - local indexing/search/watcher load-shed surfaces only; never mirror these into `.gitignore`.
- `tools/reconc/harness/template/repo-root-scaffold/.gitignore.excerpt` - gitignore entries to merge.
- `tools/reconc/harness/template/repo-root-scaffold/docs/` - starter TASK/documentation files for empty repos.
- `tools/reconc/harness/template/layout-scaffold/flat/config/arch/` - flat-root architecture rule templates that are copied only when flat-root layout is selected.
- `tools/reconc/harness/template/layout-scaffold/codebase/config/arch/` - codebase-layout architecture rule templates that are copied only when `codebase/` layout is selected.
- `tools/reconc/harness/template/repo-root-scaffold/scripts/build/` and `backend/project/` - Go CLI default skeleton for empty Go repos only.

## Step 0: Read And Inventory

From the target repo root:

1. Confirm you are in the repo root: `git rev-parse --show-toplevel` or, if not initialized, inspect the directory directly.
2. List top-level files/directories without reading protected intake folders.
3. Determine whether the repo is empty, early, or mature.
4. Determine layout:
   - `flat` if `backend/`, `frontend/`, `scripts/`, `config/`, `db/` live at root or repo is empty.
   - `codebase` if a real `codebase/` owner tree already exists.
   - `custom` if neither fits.
   - For an empty repo, tell the user the default is flat-root, briefly show `backend/`, `scripts/`, `config/`, `docs/`, `tools/`, and ask only if they want the non-default `codebase/` wrapper.
5. Determine language/stack from real files:
   - Go: `go.mod`, `*.go`, `backend/**`, `scripts/build/build.go`.
   - Rust: `Cargo.toml`, `Cargo.lock`, `src/**`, `crates/**`.
   - TS/Bun frontend: `package.json`, `bun.lock`, `frontend/**`.
   - Mixed: more than one of the above.
6. If language/stack is unclear, ask the user one concise question before writing stack-specific files.

## Step 0a: Establish The Stable User CLI

Install Reconc through the supported package manager or native installer and
verify `reconc doctor --global`. A standalone source contributor may instead
publish the current build once:

```sh
go build -o .build/bin/reconc ./cmd/reconc
.build/bin/reconc install-cli
reconc --version
```

`install-cli` performs no download. It atomically installs the exact running
build to `$RECONC_INSTALL_DIR`, `~/.local/bin` on POSIX, or
`%LOCALAPPDATA%\Programs\Reconc\bin` on Windows; rejects a symlink target; and
verifies the executable resolved by bare `reconc`. Once PATH identity passes,
it publishes a private source-ownership receipt below
`$RECONC_HOME/install/` under the same lock. It never edits shell profiles or
the parent process environment. If the directory is missing from PATH or
another binary shadows it, apply the exact emitted remediation, open a new
terminal, repeat `reconc install-cli`, and require
`reconc doctor --global` to report `healthy` before repository mutation.

Do not continue with path-qualified daily commands. Compatibility and
transactional bootstrap apply perform the same stable user-CLI installation
and verify the exact-build PATH contract before any repository write;
transactional verify checks it again.

## Step 0b: Run Canonical Transactional Init

Use one local Reconc binary for the complete non-interactive transaction:

```sh
reconc init <target-repo> --profile advanced \
  --pack <reviewed-pack> \
  --hook <selected-hook-kind> \
  --json
```

The `minimal` profile selects `.reconc.yml`, a compact managed Reconc block in
`AGENTS.md`, and the managed runtime-ignore block. The `governed` profile
additionally selects the TASK control plane, `docs/documentation.md`,
`start.md`, and the stable repo-local hook wrapper. Profile default packs are
`default` and `agent`. The `advanced` profile adds this complete immutable
public harness under `tools/reconc/harness/template/`; its exact pack version
and digest are recorded in the durable plan, private rollback receipt, and
portable `.reconc/install.lock.json` ownership receipt.
Detected stacks, pack suggestions, and agent-platform directories are evidence
only. Packs and hooks are installed only when they are named explicitly.

For a mature repository that already owns policy, agent instructions, docs,
TASK state, and ignore policy, first run `reconc refresh .`, then use
`--profile existing`. It requires that fresh lockfile and owns only selected
hooks, `tools/reconc/bin/hook`, and an optional stable binary. It rejects
`--pack` and leaves the existing control plane untouched.

Review every reported action, checksum, mode, conflict, and blocking issue.
Init records its exact durable plan path and tamper-evident receipt in the
result. Operators who require a separately reviewed plan may use the lower-level
`bootstrap inspect`, `profiles`, `plan`, `apply`, and `verify` commands against
the same engine.

Init apply is create-only. Exact existing artifacts remain unchanged. If any target
differs, no normal target is installed; hash-addressed
`*.reconc-candidate-<sha>` files are created for surgical review and apply exits
with status `drift`. Rebuild the plan after integrating or rejecting every
candidate. Marker-only AGENTS or ignore drift can be accepted only through the
exact `--accept-managed-blocks` rerun printed by the compatibility command. A
stale saved plan fails before publication and prints the exact
selection-preserving `bootstrap plan ... --replace-output` command. That flag
replaces only a valid Reconc plan for the same canonical repository. A later
failure rolls back
only transaction-owned files whose identity and checksum still match, and
removes only empty directories created by that transaction. It never removes
or overwrites an external edit.

Successful apply writes private rollback state plus the portable ownership
receipt, prints one compact artifact and hook-state summary, and emits exactly
one next command. Reverse a reviewed transaction with `reconc bootstrap remove
--plan <plan> --json` or one platform with `reconc hook uninstall <kind>
<target-repo> --json`. Removal uses portable ownership as its maximum
authority, removes only exact owned files and generated artifacts, strips only
exact managed blocks while preserving outside bytes, and emits review
candidates on drift. It never deletes user-owned policy, docs, TASKs, or
unrelated content merely because an older private receipt once created them.

After the global CLI changes, synchronize the immutable installed surfaces as
a separately reviewed transaction:

```sh
reconc repo sync plan <target-repo> --output /tmp/reconc-sync.json
reconc repo sync apply --plan /tmp/reconc-sync.json --digest <plan-digest>
reconc repo sync verify <target-repo>
```

Planning is read-only unless `--output` is supplied. Review all actions,
migrations, candidates, and blocking issues. Never invent or recompute the
digest. Apply re-plans under the repository lock and mutates only exact
`replace-owned`, `update-managed-block`, or `create-owned` actions. User drift,
orphaned legacy paths, incompatibility, or manual review block the transaction.
Registered policy migrations never rewrite `.reconc.yml`, and rollback never
overwrites a concurrent external change.

After verification, the agent itself inspects the target once with
`reconc session-briefing <target-repo> --json`. That versioned response carries
the current TASK, policy delta, and repository-run state without a Git process
or repository write.
It enables repository continuation with `reconc run on <target-repo>` only
when autonomous execution is requested, and disables it with
`reconc run off <target-repo>` on explicit stop or a real blocker. Prompt text,
runtime interrupts, session boundaries, and application restarts never mutate
the durable switch; an interrupt releases only the current host invocation.
Complete or absent TASK state disables it automatically after terminal gates.
Do not ask the user to operate these commands.

The following manual steps remain authoritative for the project harness,
stack config, conditional skeletons, architecture boundaries, project-specific
AGENTS content, and any mature-repository merge that is outside the universal
profile.

## Step 1: Scan Global Agent Rule Sources

Before editing local `AGENTS.md`, inspect these global rule sources when present:

- `~/.codex/AGENTS.md`
- `~/.config/opencode/AGENTS.md`

Read them only to extract language/style best practices relevant to the detected target stack. Do not edit them.

Decision behavior:

- If one source clearly has the best current Go/Rust/TS rules for the detected stack, propose merging that source's language/style section into target `AGENTS.md`.
- If several sources conflict, choose the newest/most complete rule set and tell the user which one you recommend.
- If no relevant language section exists, continue with only the Reconc workflow excerpt and ask the user whether they want stack-specific local rules added.
- Do not put Go rules into a Rust repo or Rust rules into a Go repo.

## Step 2: Derive The Project Harness From The Installed Pack

The advanced init transaction must already have installed
`tools/reconc/harness/template/`. Never replace it with a source checkout or a
mutable download.

1. Derive `<project-name>` from the target repo directory name normalized to lowercase/kebab-case. If the directory name is generic (`repo`, `project`, `new`) or conflicts with an existing package/module name, ask the user for the canonical project name before renaming.
2. Copy the installed template to `tools/reconc/harness/<project-name>/`; keep the immutable template intact for receipt verification and future sync.
3. Rebrand only inside `tools/reconc/harness/<project-name>/`:
   - `project` -> `<project-name>` lowercase.
   - `Project` -> `<ProjectName>` title/camel display form.
   - `PROJECT` -> `<PROJECT_NAME>` uppercase env/policy form.
   - `reconc-harness/template` -> `reconc-harness/<project-name>`.
   - `tools/reconc/harness/template` -> `tools/reconc/harness/<project-name>`.
4. If a project harness already exists, inspect and merge it surgically. Never overwrite it with the template.

## Step 3: Configure Stack

Edit target `tools/reconc/harness/<project-name>/config/workflow/stack-config.yaml`.

Default empty repo profile:

- `stack: go-cli`
- `project: <project-name>`
- `layout: auto`
- `build.enabled: true`
- `build.language: go`
- `build.require_go_mod: true`
- `build.require_build_runner: true`
- `build.require_build_runner_test: true`
- `build.require_frontend_package: false`
- `build.backend_entrypoints: [<project-name>]`
- `durable_store.enabled: false`
- `generated_references.enabled: false`
- `architecture_boundaries.required: false`
- `agent_hooks.require_codex_config: true`
- `agent_hooks.require_codex_hook_file: true`
- `agent_hooks.require_github_copilot_hooks: true`
- `agent_hooks.require_cursor_hooks: true`
- `agent_hooks.require_claude_settings: true`
- `agent_hooks.require_opencode_plugin: true`
- `agent_hooks.require_devin_hooks: true`
- `agent_hooks.require_antigravity_hooks: true`
- `agent_hooks.require_kilo_plugin: true`
- `agent_hooks.require_omp_extension: true`
- `agent_hooks.require_pi_extension: true`
- `agent_hooks.require_grok_hooks: true`

For Rust CLI:

- Set `stack: rust-cli`.
- Set `build.language: rust`.
- Set `build.require_go_mod: false`.
- Set `build.require_cargo_toml: true`.
- Set `build.require_build_runner: false` unless a real Rust build runner is copied or already exists.
- Set `build.backend_entrypoints: []`.
- Do not copy `backend/project/` or `scripts/build/build.go` from the Go default skeleton.

For Go fullstack/frontend/durable-store repos:

- Enable only the surfaces that are actually selected.
- `build.require_frontend_package: true` only when the repo intentionally has a Bun frontend workspace.
- `durable_store.enabled: true` only when the repo intentionally has durable SQLite store and migrations.
- `generated_references.enabled: true` only when the repo intentionally has generated reference contracts and generator.
- `architecture_boundaries.required: true` only after `config/arch/arch-rules.yaml` or `codebase/config/arch/arch-rules.yaml` is installed and matches the repo layout.

Stack config controls enforcement. Do not leave a selected surface disabled just because bootstrapping was inconvenient. If the repo is meant to have a build runner, durable store, frontend, generated references, or arch rules, install the real files and enable the check.

Before selecting policy packs, inspect the target and, when individual rule
suggestions are also useful, run both read-only discovery surfaces:

```sh
reconc bootstrap inspect . --json
reconc adopt . --json
```

Treat `pack_suggestions` from either command as evidence-backed candidates, never automatic
configuration. Inspect each pack manifest's capabilities, inputs, evidence,
rules, and conflicts. Add a pack to `.reconc.yml` `extends` only when the real
repository stack and control intent match. All stack packs (`go-assurance`,
`bun-assurance`, `python-assurance`, `rust-assurance`, `shell-assurance`,
`cpp-assurance`, `java-assurance`, `php-assurance`, `csharp-assurance`,
`npm-assurance`, `pnpm-assurance`, `yarn-assurance`,
`typescript-assurance`, `nextjs-assurance`, `svelte-assurance`,
`zig-assurance`, `elixir-assurance`, and `powershell-assurance`)
start in warn mode so a new rollout can measure friction before explicitly
tightening selected repo-local rules. Packs consume native scans and successful
command evidence; they do not install or invoke a target toolchain. Never copy
source-harness-specific gate paths, baselines, exemptions, or proof ledgers
into a target repo.
For Node.js targets, accept a manager only from one unambiguous lockfile or
`packageManager` declaration at the relevant package boundary. Never invent a
test, lint, build, or typecheck command from source files or `tsconfig` alone.
The npm, pnpm, and Yarn packs require current evidence only for scripts the
package actually declares. Select `typescript-assurance` for generic
TypeScript, or the matching Next.js/Svelte pack for those frameworks, never
both generic and framework ownership.
`go-assurance` is only for repositories with real Go stack evidence. Its native
changed-file gates enforce canonical Go formatting and flag bare goroutine
launches unless the same function proves `WaitGroup.Add`, deferred
`WaitGroup.Done`, and `WaitGroup.Wait` ownership, without spawning tools;
it has no value in a non-Go project and must not be selected merely because the
Reconc binary itself is written in Go.

## Step 4: Deploy Repo Root Scaffold

Work from `tools/reconc/harness/<project-name>/repo-root-scaffold/`.

If Step 0a already applied the `governed` profile, do not copy its exact
universal targets again. Compare the remaining project harness scaffold to the
installed files and merge only the additional project-specific content.

First sync generated hook artifacts from the repo-local Reconc generator:

```sh
tools/reconc/dist/<local-reconc-binary> hook sync-scaffold tools/reconc/harness/<project-name>/repo-root-scaffold
```

This writes `.githooks/pre-commit`, `.codex/hooks.json`, `.github/hooks/reconc.json`, `.cursor/hooks.json`, `.agents/hooks.json`, `.claude/settings.json`, `.opencode/plugins/reconc.js`, `.devin/hooks.v1.json`, `.kilo/plugin/reconc.js`, `.omp/extensions/reconc.ts`, `.pi/extensions/reconc.ts`, and `.grok/hooks/reconc.json` from the same generator used by `reconc hook install`. Do not edit these hook artifacts manually and do not copy them from any source-specific harness.

Direct-copy files when the target file is missing:

- `start.md`
- `.reconc.yml`
- `.githooks/pre-commit`
- `.codex/config.toml`
- `.codex/hooks.json`
- `.cursor/hooks.json`
- `.agents/hooks.json`
- `.claude/settings.json`
- `.opencode/plugins/reconc.js`
- `.devin/hooks.v1.json`
- `.kilo/plugin/reconc.js`
- `.omp/extensions/reconc.ts`
- `.pi/extensions/reconc.ts`
- `.grok/hooks/reconc.json`
- `.cursorindexingignore`
- `.codeiumignore`
- `.windsurfignore`
- `.ignore`
- `.vscode/settings.json`
- `docs/tasks.md`
- `docs/tasks/TASK-0001-Bootstrap-Reconc.md`
- `docs/documentation.md`

Note on TASK grammar: this harness ships the logbook-v1 profile
(`Current:` header plus `TASK-NNNN-Name` rows) and declares it in the
scaffold `.reconc.yml`. The `reconc bootstrap` governed profile emits
the sections-v1 grammar instead. Both are first-class reconc profiles;
never mix the two grammars in one overview file.

Merge-only files:

- `AGENTS.md`
- `.gitignore.excerpt`

Stack-conditional files:

- Copy `backend/project/` only for a new Go CLI/default repo.
- Copy `scripts/build/` only for a Go repo that will use this default Go build runner.
- Copy `layout-scaffold/flat/config/arch/` to target `config/arch/` for a flat-root repo that wants architecture boundaries.
- Copy `layout-scaffold/codebase/config/arch/` to target `codebase/config/arch/` for a `codebase/` repo that wants architecture boundaries.
- Do not manually rewrite architecture path tokens between layouts. Use the matching scaffold tree for the selected layout.
- Never copy both architecture scaffold trees into the same target repo.

Never copy these as generic defaults:

- `go.mod`
- `codebase/frontend/package.json`
- Secondary/internal-only binary paths.
- SQLite initial migration.
- generated_reference contracts/generator.

## Step 5: Merge AGENTS.md Excerpt

If target `AGENTS.md` does not exist:

1. Copy scaffold `AGENTS.md`.
2. Rebrand `project`/`Project`/`PROJECT`.
3. Add language/style sections from the selected global source only after user confirmation when needed.

If target `AGENTS.md` exists:

1. Read the full file.
2. Insert the scaffold workflow excerpt without deleting existing project-specific rules.
3. Remove duplicate/conflicting old workflow rules only after you can prove the new Reconc excerpt supersedes them.
4. Do not insert excluded source-project-specific sections:
   - Product Naming.
   - Product Architecture Baseline.
   - Stack Overrides.
   - Go rules & Pattern.
   - Product Code Style Compression.
   - Concept And Decisions.

The excerpt must preserve these workflow sections:

- Scope.
- Standalone Working Contract.
- File Operations.
- Test Integrity.
- AI Workflow.
- Repository Run.
- Source Of Truth.
- Reality Check Standard.
- `docs/spec.md` Discipline.
- Research References.
- Glossary.
- Repo Structure, generalized for flat-root and `codebase/`.
- Project Bootstrap.
- Tooling Policy.
- TASK Lifecycle.
- Go File Size Budget.
- Codebase Hygiene.
- File And Path Conventions.
- Final Local Checklist.

## Step 6: Merge .gitignore Excerpt

If the target repo is not initialized:

1. Run `git init` first if the user wants the repo governed immediately.
2. Create `.gitignore` if missing.

Then merge `.gitignore.excerpt` into `.gitignore` without deleting existing entries.

Required Reconc runtime ignores:

- `/tools/reconc/dist/`
- `.reconc/*`
- `!.reconc/`
- `!.reconc/install.lock.json`
- `!.reconc/policy.lock.json`
- `.reconc/audit.jsonl*`
- `.reconc/cache/`
- `.reconc/locks/`
- `.reconc/reports/`
- `.reconc/run/`
- `.reconc/sessions/`
- `.reconc/task-transaction.json`
- `.reconc/bootstrap-*.json`
- `*.reconc-candidate-*`
- `*.reconc-remove-candidate-*`

`.reconc/run/` holds repo-local run state: `state.bin` and the bounded,
transition-only `decisions.jsonl`. It is gitignored above. No per-repo
scaffolding is needed: `reconc run on|off|status|log` ships in the binary and
operates this directory in any repo. The agent operates the switch. The per-TASK
Reality-Check loop (`docs/task-loop-workflow.md`, scaffolded) and its AGENTS.md
excerpt are merged into the repo's `AGENTS.md` like the other sections.

Reconc owns runtime retention in the product binary. SessionStart and
SessionEnd perform a cheap six-hour due check; Stop never performs cleanup.
The same pass is available to an agent as `reconc prune . --json` and can be
inspected without mutation via `reconc prune . --dry-run --json`. It bounds
product-wide project roots, session/report/lock/command-proof state, audit and
run-decision JSONL rings, generated audit binaries, abandoned atomic/build
temps, and owned `reconc-proof-*` temp trees.
Proof temp trees use a two-hour inactivity grace to bound hard-kill residue
while preserving recent work.
Do not add another project-specific cleanup loop or attach cleanup to the
workflow-audit cache. Active state and live build targets are protected even
when that means reporting a temporary budget excess.
Workflow-audit Git subprocesses have a 15-second deadline; generated-reference
build and execution have a two-minute deadline, and canceled commands have a
two-second process/pipe wait bound. Preserve those bounds in derived harnesses.

Dual-layout build/dependency ignores should cover both flat-root and `codebase/` when relevant:

- `build/`
- `codebase/build/`
- `frontend/node_modules/`
- `codebase/frontend/node_modules/`
- `frontend/dist/`
- `codebase/frontend/dist/`
- `frontend/.vite/`
- `codebase/frontend/.vite/`
- `frontend/.next/`
- `codebase/frontend/.next/`

## Step 6b: Local Indexing and Watcher Load-Shed

These files are local-tool performance controls, not Git/GitHub ignores:

- `.cursorindexingignore` controls Cursor semantic indexing breadth.
- `.codeiumignore` controls Windsurf/Codeium local indexing breadth.
- `.windsurfignore` is a compatibility twin for Windsurf builds that probe a Windsurf-named ignore file; `.codeiumignore` remains the official local-index source.
- `.ignore` controls local ripgrep-style developer search only. Agents doing forensic full-tree searches must use `rg --no-ignore` when the excluded trees are intentionally in scope.
- `.vscode/settings.json` controls VS Code/Cursor file watcher and search excludes.

Heavy reference and runtime trees (`research/**`, `_drop/**`, `.reconc/**`, generated spec-audit claim/range artifacts, build/cache/dependency outputs) stay tracked or untracked exactly as before. These files must not be copied into `.gitignore` and must not change GitHub sync semantics. `_drop/` is an explicit manual intake zone and remains agent-taboo unless the user names it.

## Step 7: Hook Behavior

The scaffold hook configs must first call the repo-local wrapper:

- `tools/reconc/bin/hook`

Hook files in `repo-root-scaffold/` are generated with `reconc hook sync-scaffold`. The typed registry is the source of truth for Claude Code, Codex, GitHub Copilot, Cursor, OpenCode, Devin CLI, Antigravity CLI, Kilo Code, Oh My Pi, Pi, Grok Build, and the source-controlled `.githooks/pre-commit` twin. Root repo artifacts and template scaffold artifacts must never be reconciled by copying from each other; both are regenerated from the same Reconc binary. A target repo must never compare against, depend on, or copy from a source-specific harness.

Hook installation and scaffold sync reject any target whose existing parent symlinks resolve outside the selected repository. Scaffold sync preflights every target before writing, preventing partial rollout. Forced malformed-config backups remain private and crash-durable.

After copying or installing hooks, run:

```sh
tools/reconc/dist/<local-reconc-binary> hook status . --json
```

Treat `degraded`, `shadowed`, and `unsupported` as unresolved rollout defects.
`installed` is valid only for an artifact that intentionally still needs its
documented activation switch. `configured` means the static config is complete
and host-discoverable, not loaded or enforced. `expected_events`,
`live_events`, `unseen_events`, `last_seen`, and `last_event` are separate
live-process proof; each route is rate-limited to one external-state write
every six hours. Treat one route as `observed` only when it appears in
`live_events`; registry-derived `surface_events` lists documented per-surface
routes without claiming execution. Treat a pre-action route as `enforced` only
after a disposable negative probe proves the side effect did not occur.
Inferred lifecycle, such as OpenCode/Kilo `session.idle` or Pi
`agent_settled`, never becomes a
native Stop claim.

Claude Code generated hooks use exec-form `command` plus `args`, pass `${CLAUDE_PROJECT_DIR}` directly to the wrapper, and use the context-capable `SessionStart` `compact` matcher for recovery instead of spawning the notification-only `PostCompact` event. Codex bootstrap and direct install manage `hooks = true` under `[features]`; root-level `hooks=true` is invalid. Direct install rejects an explicit user `hooks = false` before any hook write unless `--force` is supplied. Transactional bootstrap exposes the same change as managed drift and requires explicit marker-only acceptance. Uninstall restores the exact original line. Codex has no `SessionEnd` event. GitHub Copilot uses `.github/hooks/reconc.json`; Copilot CLI and coding agent share the version-1 repository contract, but `PermissionRequest` and `Notification` are CLI-only and every host timeout remains fail-open. A foreign file at the managed path is never overwritten, including with `--force`. Cursor's one `.cursor/hooks.json` is shared configuration, not proof of identical Agent, Cmd+K, Tab, CLI, print, or cloud event delivery. Its `postToolUse` route is successful generic evidence, `postToolUseFailure` is failure only, and `afterShellExecution` is passive because that payload has no exit status. Devin uses `.devin/hooks.v1.json`, passes `DEVIN_PROJECT_DIR`, and includes `PostCompaction`. OpenCode and Kilo Code plugins are transport adapters only: policy, session state, continuation, and context recovery remain in the Go runtime. Shell success requires integer `output.metadata.exit == 0`. Their bounded asynchronous continuation trigger is inferred from `session.idle`, not a synchronous native Stop gate, and never falls back to synchronous prompt submission. Kilo Code requires `KILO_PURE` to be unset so project plugins load. Oh My Pi loads the typed project extension `.omp/extensions/reconc.ts`. Native `tool_call` and awaited main-session `session_stop` are fail-closed boundaries; approval, outcome, compaction, and shutdown routes are observational. Tool outcome follows exact `isError`, with synthetic exit code zero only for a successful built-in `Bash` call. Stop continuation is capped at eight accepted requests per session, and task/subagent sessions never enter the Stop route. Pi loads `.pi/extensions/reconc.ts` only after project trust. Reconc never edits trust; native `tool_call` and `user_bash` are fail-closed, while outcomes, lifecycle, compaction, and shutdown are observational. Inferred `agent_settled` continuation is capped at ten requested messages per session, and `sendUserMessage` provides no delivery acknowledgement. OpenCode, Kilo Code, OMP, and Pi own one repository-scoped Reconc child per live plugin instance. They exchange bounded format-1 NDJSON requests over stdio in deterministic order, kill the worker on cancellation or timeout, and use the remaining route budget for one-shot recovery after startup, crash, or protocol failure. Shutdown or parent stdin closure prevents orphan workers. No daemon, socket, listener, or network call is introduced.

Cursor CLI uses the primary `agent` command; `cursor-agent` is its compatibility
alias. The documented CLI surface is registry-owned instead of inferred from
the desktop artifact. `workspaceOpen` is sessionless liveness only and returns
no plugin paths. Cursor's missing generic hooks for `AskQuestion` cannot be
reconstructed by Reconc.

Grok Build loads `.grok/hooks/reconc.json` only after project trust is granted with `/hooks-trust` or `--trust`. Native PreToolUse is a hard explicit allow/deny boundary. Reconc also emits exact native Stop block JSON without a leader, marks eligible live Stops strict, uses a 600-second Stop budget, and leaves user interrupts plus `channel_closed`/`shutdown` untouched. It accepts synchronous native enforcement only when the hook guide shipped with the installed Grok distribution explicitly advertises blocking Stop decision control, never from the version string. The generated wrapper converts missing/broken/ambiguous binaries, malformed payloads, runtime failures, and invalid output into deny/block JSON while it can still respond; a host timeout or OS kill before output remains fail-open. Passive Stop distributions can use optional leader fallback over the Unix socket or Windows named pipe. Protocol-1 `_x.ai/interject` attempts are bounded to 32 delivered no-progress continuations and reset on material progress, a new block, or a clean Stop; capability-proven native hosts suppress duplicate interjection. `RECONC_GROK_STEER=0` disables only leader steering. Deep doctor reports native Stop capability separately and probes optional leader protocol plus `_x.ai/interject`.

The registry assigns 5-second observation/session timeouts, 10-second pre-tool/permission timeouts, and platform-specific Stop budgets. Grok uses its native 600-second Stop default; ordinary synchronous platforms use 30 seconds. OpenCode, Kilo Code, OMP, and Pi adapters enforce their budgets internally. OMP uses a 29-second internal Stop budget inside the host's 30-second extension-handler deadline and a one-second shutdown observation budget inside the host's two-second shutdown limit. All four adapters concurrently drain both pipes, kill and await slow subprocesses, cap combined output at 8 KiB, reject invalid UTF-8 or truncated decision JSON, and never embed a versioned release filename. OpenCode and Kilo async continuation state is capped at 1,024 sessions and ten accepted requests per session; OMP's native Stop continuation is capped at eight; Pi state is capped at 1,024 sessions and ten requested continuations. Generated Claude, Codex, GitHub Copilot, Cursor, Devin, Antigravity, and Grok configs do not spawn PreToolUse for read-only matchers; read evidence remains in authoritative PostToolUse while pre-execution hooks stay focused on write/shell/apply_patch policy checks. Shell-command runtimes first exec `./tools/reconc/bin/hook` directly when their cwd is already the repo root, and only fall back to `git rev-parse` plus `RECONC_HOOK_REPO_RESOLVED=1` when needed. The agent-hooks audit rejects git-first launchers, Claude/Devin shell/git launchers, project-specific OpenCode/Kilo Code/OMP/Pi state logic, version-pinned OpenCode/Kilo Code/OMP/Pi binaries, stale hook timeouts, and wrapper configs that omit the direct-wrapper fast path. The wrapper trusts the resolved marker or an already-valid repo-local wrapper/dist path, normalizes only direct/manual calls, and execs the first available repo-local Reconc binary. An owned current-host stable binary also publishes `tools/reconc/bin/hook-target`; the wrapper validates and reads that exact one-line direct target without platform discovery, directory scans, version-glob expansion, or PATH lookup. Missing, invalid, symlinked, or non-executable direct targets enter the portable unambiguous resolver. The Go hook runtime lowers observation-only hook priority on Unix for post/after/session-end events; PreToolUse, permission and Stop keep normal priority. The final `exec` keeps hook process trees shallow and avoids idle parent shells where the host runtime allows it.

The wrapper resolves binaries without pinning a Reconc release number. It
tries this exact order:

1. Development binary `.build/bin/reconc`.
2. Development/self-host binary `reconc` at repo root.
3. Validated direct target from `tools/reconc/bin/hook-target`.
4. Stable `tools/reconc/dist/reconc-<os>-<arch>[.exe]`.
5. Exactly one compatible versioned artifact under `tools/reconc/dist/`.
6. The same stable-then-unambiguous lookup under root `dist/`.
7. `reconc` on PATH.

The direct-target receipt is transactionally owned with the wrapper and exact
current-host stable binary. Cross-platform plans omit it. More than one compatible versioned artifact in a searched directory is an
ambiguity error. Install the stable name or retain exactly one versioned
fallback; never select a release by directory order. The first two candidates
avoid OS/architecture subprocess probes on development and self-hosting paths.

Hooks do not depend on the user CLI: their PATH fallback remains last after
development and repo-local binaries. The separate user CLI established in
Step 0a is the stable interactive/operator command. POSIX routes in generated
JSON hook configs must not inline binary fallback loops; they call
`tools/reconc/bin/hook` and let the wrapper own binary selection. PreToolUse,
permission and Stop hooks remain hard/interactive priority; only observation
hooks are lowered.

On native Windows, generated shell hook routes, the OpenCode/Kilo/OMP/Pi transport to
the extensionless Reconc wrapper, and `.sh` or extensionless policy scripts
require `sh` on `PATH`; Git for Windows supplies it. Native `.exe` and `.com`
policy scripts execute directly. Do not invent a per-project wrapper variant.

### Step 7a: Assert TASK-scoped claims

Run the task-claim helper from the target repository root through its root-safe
launcher. The command name comes first, followed by the optional validated TASK
override:

```sh
tools/reconc/harness/<project-name>/utils/task-claim/run-task-claim show
tools/reconc/harness/<project-name>/utils/task-claim/run-task-claim assert --task TASK-0001-Bootstrap-Reconc
```

The launcher enters the owning nested Go module before starting the helper. The
helper then walks upward and accepts a root only when both `docs/tasks.md` and
the project claim bindings exist. Do not use the root-module form
`go run ./tools/reconc/harness/.../utils/task-claim`; it cannot cross the nested
Go module boundary.

### Step 7b: Activate source-controlled git hooks

The repo-root scaffold ships `.githooks/pre-commit`. This is the
source-controlled twin selected once `core.hooksPath` points at `.githooks`;
`reconc hook install git-pre-commit` resolves and updates that same active path
rather than writing an inactive `.git/hooks` copy. It runs `reconc ci --staged` so that
commits made via Bash (not just agent-runtime hooks) are also gated by the
compiled policy lockfile. This closes the gap where an agent could `git commit`
before the agent-runtime Stop hook fires. Required successful commands first
run through `reconc exec . --staged -- COMMAND`; bounded receipts are tied to
the exact HEAD and staged index independently of agent-runtime PostToolUse
coverage.

Activate it once per fresh clone:

```sh
git config core.hooksPath .githooks
chmod +x .githooks/pre-commit
```

`.git/hooks/pre-commit` (if any was previously installed) is shadowed by `core.hooksPath` and may be removed. The `project-git-hooks-audit` rule warns (does not block) until `core.hooksPath` is set to `.githooks`.

## Step 8: Rebrand Root Scaffold

After copying or merging:

1. Replace `project` with the lowercase project name where it is a placeholder.
2. Replace `Project` with the display/title project name where it is a placeholder.
3. Replace `PROJECT` with the uppercase environment/policy form where it is a placeholder.
4. Do not blindly replace words inside third-party prose, URLs, external references, or existing project-owned names.
5. Search after replacement for stale placeholder names, source-specific product names, source-specific internal binaries, source-specific UI names, and hardcoded local user paths.
6. Remaining stale hits must be fixed before continuing unless they are immutable external references that the target repo intentionally owns.

## Step 9: Verify

Use bare `reconc` for interactive verification. The repo-local wrapper still
proves hook-runtime resolution independently.

Required checks:

1. `reconc --version` and `reconc run status` from the target repository root.
2. `reconc bootstrap verify --plan <plan-path-from-init> --json` when the transactional profile was used.
3. `reconc repo sync verify . --json`
4. `reconc hook status . --json`
5. `reconc session-briefing . --json`
6. `cd tools/reconc/harness/<project-name> && go test ./...`
7. `tools/reconc/harness/<project-name>/audits/run-workflow-audit all`
8. Selected stack build/test commands:
   - Go default: `go test ./...` and `go run ./scripts/build validate` if the build runner was installed.
   - Rust: `cargo fmt --check`, `cargo clippy -- -D warnings`, `cargo test` when Rust is selected.
   - Frontend: selected package manager checks only when a frontend stack is selected.

If an audit is disabled by stack config, record why. Disabled is allowed only when that surface is not part of the target stack. Disabled is not allowed as a permanent shortcut for a surface the repo is supposed to have.

## Step 10: Finish Bootstrap TASK

Update the active bootstrap TASK. The governed CLI profile creates
`docs/tasks/001-bootstrap-reconc.md`; a project harness that already owns the
legacy template name may retain `docs/tasks/TASK-0001-Bootstrap-Reconc.md`:

- Record files copied.
- Record files merged.
- Record selected stack and layout.
- Record policy-pack recommendations reviewed, packs selected, and the evidence/rationale for each decision.
- Record global rule sources inspected and which language/style source was proposed or merged.
- Record every verification command and result.
- Record any stack-intentional disabled checks.
- Complete `## Final Reality Check`.

Then update `docs/tasks.md` according to the TASK lifecycle. Commit once when the bootstrap TASK is genuinely done.

## Existing Mature Repo Path

If the target repo is mature:

1. Do not force flat-root or `codebase/`.
2. Inventory real owners and existing build/test/docs/task systems.
3. Identify which Reconc rules can be adopted directly.
4. Identify which rules need stack-config or path adaptation.
5. Compile the reviewed repository-owned policy with `reconc refresh .`.
6. Use `bootstrap plan --profile existing` for hooks, wrapper, and optional binary wiring.
7. Present a compact plan to the user before changing project-specific files.
8. Prefer adapting `.reconc.yml`, `stack-config.yaml`, and `AGENTS.md` to the repo over reorganizing the repo.
9. Never delete or rename existing docs/tasks/start/config files without explicit approval.

## Final Bootstrap Reality Check

The rollout is not done until all of this is true:

- No stale placeholder names remain.
- Bare `reconc` resolves to the exact build used for bootstrap; `reconc run status` works from the repository root without a path or explicit `.` argument.
- The reviewed bootstrap plan matches the applied profile, packs, hooks, binary checksum, and target platform.
- `reconc bootstrap verify --plan ... --json` passes and no unresolved candidate file remains.
- `.reconc/install.lock.json` is committed, self-digested, and `reconc repo sync verify . --json` passes.
- No source-specific product, internal-binary, UI, or local-machine text remains in generic runtime or workflow files.
- `.reconc.yml` points to `tools/reconc/harness/<project-name>/...`.
- `stack-config.yaml` matches the selected stack.
- Every selected policy pack matches real stack evidence; no recommendation was silently auto-applied.
- Hooks prefer development/self-host binaries without platform probes, then the validated install-time direct target, then recovered local dist binaries on macOS/Linux/Windows, before PATH.
- `repo-root-scaffold/` hook artifacts were synced with `reconc hook sync-scaffold` from the local generator; no hook artifact was edited by hand or copied from a source-specific harness.
- POSIX hook routes call `tools/reconc/bin/hook` first and retain local-dist/PATH fallback; native Windows shell routes have `sh` on `PATH`, and native `.exe`/`.com` policy scripts execute directly.
- `hook status . --json` reports every selected platform as `configured`; no platform is degraded, shadowed, unsupported, or accidentally left only installed.
- OpenCode, Kilo Code, OMP, and Pi extensions contain no project-specific run state or prompts; Antigravity contains no blanket 120-second timeout.
- When Grok is selected, `reconc doctor --deep` proves project trust, project-owned inspect metadata, every exact native route, and whether the installed Grok guide advertises native no-leader Stop; passive distributions use optional leader fallback after protocol-1 `_x.ai/interject` verification.
- Cursor/Windsurf/Codeium/VS Code indexing excludes are installed as local-tool performance controls only, not Git ignores.
- `AGENTS.md` contains the workflow excerpt and any user-approved stack-specific style rules.
- `.gitignore` contains Reconc runtime ignores and relevant dual-layout build/dependency ignores.
- Repo-local binaries under `tools/reconc/dist/` are ignored while `tools/reconc/bin/hook` remains source-controlled.
- `docs/tasks.md` and the active TASK detail file pass task-state audit.
- `session-briefing . --json` returns a supported `format_version`, current
  TASK/policy delta, and repository-run state without a redundant status call.
- `run-workflow-audit all` passes, or any disabled surface is explicitly disabled because it is not part of the selected stack.
