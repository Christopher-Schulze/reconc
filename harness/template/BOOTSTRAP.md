# Reconc Template Bootstrap

This file is the authoritative rollout runbook for a fresh agent that installs this Reconc workflow package into another repository.

Read this file completely before touching files. The goal is not to invent a new workflow. The goal is to install this portable Reconc/Policy/Hook/Start/TASK governance package into the target repository using `project` placeholders and `stack-config.yaml`.

## Non-Negotiable Contract

- Work only under the target repository. Do not edit global agent files.
- Copy files from this template. Do not move anything out of the source template.
- Do not overwrite existing target-repo files. Merge excerpts surgically.
- Default for new/empty repositories is flat-root: no `codebase/`.
- Existing repositories win. If the repo is mature, analyze it and adapt Reconc to the repo instead of reshaping the repo.
- `tools/reconc/harness/template/` remains the source template folder in the toolkit.
- In the target repo, rename `tools/reconc/harness/template/` to `tools/reconc/harness/<project-name>/`, where `<project-name>` is the target repo directory name normalized to lowercase/kebab-case unless the user explicitly chooses another project name.
- Placeholder is exactly `project` / `Project` / `PROJECT`. No other project placeholder is valid.
- `AGENTS.md` is an excerpt merge: insert the workflow excerpt into an existing `AGENTS.md`; create a new one only when none exists.
- `.gitignore.excerpt` is an excerpt merge after git initialization; do not overwrite `.gitignore`.
- Do not scaffold source-project-specific surfaces into generic repos: no secondary/internal-only binary, no Bun frontend package unless the stack requires frontend, no SQLite initial migration unless durable store is selected, no generated_reference artifacts unless generated references are selected, no `go.mod` unless the target stack/repo is Go.
- Never treat ignored `docs/todo*` or changelog scratch as TASK truth or rollout input. Current TASK truth comes from the target repository's adopted `docs/tasks.md` control plane.
- Template audits are dual-path compatible: they understand both flat-root (`backend/`, `scripts/`, `config/`) and `codebase/` layout.
- Source-specific harness folders are not part of a target rollout. A standalone toolkit copy should carry the template harness and the target repo's renamed harness only.
- Hook artifacts are generated artifacts, not hand-maintained source. The canonical source is `reconc hook generate`; before copying hook files from `repo-root-scaffold/`, sync that scaffold with `reconc hook sync-scaffold tools/reconc/harness/<project-name>/repo-root-scaffold`.
- Prefer the transactional `reconc bootstrap inspect|plan|apply|verify` flow for every universal surface it owns. Use the manual sections in this runbook for project-specific harness, stack, architecture, and merge decisions that the universal CLI intentionally cannot infer.

## Source Package

The template package is expected to contain:

- `tools/reconc/` - Reconc engine, dist binaries, skills, stdlib and harnesses.
- `tools/reconc/harness/template/` - generalized harness logic.
- `tools/reconc/harness/template/config/workflow/stack-config.yaml` - stack/layout contract used by template audits.
- `tools/reconc/harness/template/repo-root-scaffold/` - files that are copied or merged into the target repo root.
- `tools/reconc/harness/template/repo-root-scaffold/AGENTS.md` - workflow excerpt, not necessarily the whole target AGENTS file.
- `tools/reconc/harness/template/repo-root-scaffold/start.md` - onboarding entrypoint.
- `tools/reconc/harness/template/repo-root-scaffold/.reconc.yml` - Reconc rules wired to `tools/reconc/harness/project/...`.
- `tools/reconc/harness/template/repo-root-scaffold/.codex/`, `.cursor/`, `.agents/`, `.claude/`, `.opencode/`, `.devin/`, `.kilo/`, `.grok/`, `.githooks/` - generated local hook/plugin configs and source-controlled git hook twin.
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

## Step 0a: Build The Transactional Bootstrap Plan

Use one local Reconc binary for the complete transaction. Inspection and plan
generation are read-only unless `--output` is explicitly supplied:

```sh
reconc bootstrap inspect <target-repo> --json
reconc bootstrap profiles --json
reconc bootstrap plan <target-repo> --profile governed \
  --pack <reviewed-pack> \
  --hook <selected-hook-kind> \
  --install-binary \
  --output <target-repo>/.reconc/bootstrap-plan.json \
  --json
```

The `minimal` profile selects `.reconc.yml` plus a compact managed Reconc block
in `AGENTS.md`. The `governed` profile additionally selects the TASK control
plane, `docs/documentation.md`, `start.md`, runtime ignores, and the stable
repo-local hook wrapper. Profile default packs are `default` and `agent`.
Detected stacks, pack suggestions, and agent-platform directories are evidence
only. Packs and hooks are installed only when they are named explicitly.

For a mature repository that already owns policy, agent instructions, docs,
TASK state, and ignore policy, first run `reconc refresh .`, then use
`--profile existing`. It requires that fresh lockfile and owns only selected
hooks, `tools/reconc/bin/hook`, and an optional stable binary. It rejects
`--pack` and leaves the existing control plane untouched.

Review every manifest action, checksum, mode, conflict, and blocking issue.
For an explicit external binary use `--binary PATH --checksum SHA256` and add
`--platform OS/ARCH` only for a cross-platform artifact. The transaction never
downloads at runtime. `--install-binary` copies the already-running executable
to the stable `tools/reconc/dist/reconc-<os>-<arch>[.exe]` path.

Apply only the exact reviewed plan, then verify it read-only:

```sh
reconc bootstrap apply --plan <target-repo>/.reconc/bootstrap-plan.json --json
reconc bootstrap verify --plan <target-repo>/.reconc/bootstrap-plan.json --json
```

Apply is create-only. Exact existing artifacts remain unchanged. If any target
differs, no normal target is installed; hash-addressed
`*.reconc-candidate-<sha>` files are created for surgical review and apply exits
with status `drift`. Rebuild the plan after integrating or rejecting every
candidate. A stale plan fails before publication. A later failure rolls back
only transaction-owned files whose identity and checksum still match, and
removes only empty directories created by that transaction. It never removes
or overwrites an external edit.

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

## Step 2: Copy Reconc Into Target Repo

If the target repo does not already have `tools/reconc/`:

1. Copy the entire `tools/reconc/` directory into target `tools/reconc/`.
2. If the copied toolkit includes any source-specific harness folder besides `template/`, remove that copied source-specific harness from the target repo unless the target repo explicitly owns it.
3. Derive `<project-name>` from the target repo directory name normalized to lowercase/kebab-case. If the directory name is generic (`repo`, `project`, `new`) or conflicts with an existing package/module name, ask the user for the canonical project name before renaming.
4. Rename `tools/reconc/harness/template/` to `tools/reconc/harness/<project-name>/`.
5. Rebrand inside `tools/reconc/harness/<project-name>/`:
   - `project` -> `<project-name>` lowercase.
   - `Project` -> `<ProjectName>` title/camel display form.
   - `PROJECT` -> `<PROJECT_NAME>` uppercase env/policy form.
   - `reconc-harness/template` -> `reconc-harness/<project-name>`.
   - `tools/reconc/harness/template` -> `tools/reconc/harness/<project-name>`.
6. Do not rebrand source-specific harness folders in the source repo. They are not part of generic rollout.

If the target repo already has `tools/reconc/`:

1. Inspect it first.
2. Compare existing Reconc version and harness paths.
3. Do not overwrite. Merge or replace only after you can explain exactly what changes and why.

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
- `agent_hooks.require_cursor_hooks: true`
- `agent_hooks.require_claude_settings: true`
- `agent_hooks.require_opencode_plugin: true`
- `agent_hooks.require_devin_hooks: true`
- `agent_hooks.require_antigravity_hooks: true`
- `agent_hooks.require_kilo_plugin: true`

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
repository stack and control intent match. `go-assurance` and `bun-assurance`
start in warn mode so a new rollout can measure friction before explicitly
tightening selected repo-local rules. Never copy source-harness-specific gate
paths, baselines, exemptions, or proof ledgers into a target repo.
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

This writes `.githooks/pre-commit`, `.codex/hooks.json`, `.cursor/hooks.json`, `.agents/hooks.json`, `.claude/settings.json`, `.opencode/plugins/reconc.js`, `.devin/hooks.v1.json`, `.kilo/plugin/reconc.js`, and `.grok/hooks/reconc.json` from the same generator used by `reconc hook install`. Do not edit these hook artifacts manually and do not copy them from any source-specific harness.

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
product-wide project roots, session/report/lock state, audit and run-decision JSONL rings, generated audit
binaries, abandoned atomic/build temps, and owned `reconc-proof-*` temp trees.
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

Hook files in `repo-root-scaffold/` are generated with `reconc hook sync-scaffold`. The typed registry is the source of truth for Claude Code, Codex, Cursor, OpenCode, Devin CLI, Antigravity CLI, Kilo Code, Grok Build, and the source-controlled `.githooks/pre-commit` twin. Root repo artifacts and template scaffold artifacts must never be reconciled by copying from each other; both are regenerated from the same Reconc binary. A target repo must never compare against, depend on, or copy from a source-specific harness.

After copying or installing hooks, run:

```sh
tools/reconc/dist/<local-reconc-binary> hook status . --json
```

Treat `degraded`, `shadowed`, and `unsupported` as unresolved rollout defects.
`installed` is valid only for an artifact that intentionally still needs its
documented activation switch. `configured` means the static config is complete
and host-discoverable. `expected_events`, `live_events`, `unseen_events`,
`last_seen`, and `last_event` are separate live-process proof; each route is
rate-limited to one external-state write every six hours.

Claude Code generated hooks use exec-form `command` plus `args`, pass `${CLAUDE_PROJECT_DIR}` directly to the wrapper, and use the context-capable `SessionStart` `compact` matcher for recovery instead of spawning the notification-only `PostCompact` event. Codex bootstrap writes `hooks = true` under `[features]`; root-level `hooks=true` is invalid, and Codex has no `SessionEnd` event. Devin uses `.devin/hooks.v1.json`, passes `DEVIN_PROJECT_DIR`, and includes `PostCompaction`. OpenCode and Kilo Code plugins are transport adapters only: policy, session state, continuation, and context recovery remain in the Go runtime. Their continuation trigger is inferred from `session.idle`, not a synchronous native Stop gate. Kilo Code requires `KILO_PURE` to be unset so project plugins load.

Grok Build loads `.grok/hooks/reconc.json` only after project trust is granted with `/hooks-trust` or `--trust`. Its native PreToolUse event is a hard explicit allow/deny boundary, but its native Stop event is passive and Grok treats hook crashes, malformed output, and timeouts as fail-open. The generated wrapper therefore runs a bounded guard and converts non-zero, timed-out, empty, multiline, or non-exact Reconc output into valid Grok deny JSON. Use `reconc grok . --prompt "..."` when strict same-session continuation is required without modifying the Grok binary. Its preflight requires generator-exact hook and executable wrapper artifacts, project-owned inspect metadata, project trust, and all 14 exact route command tokens. When Grok runs in leader mode (`grok --leader` or config `use_leader`), the Stop route additionally steers the live TUI session over the Unix leader socket or Windows named pipe. Eligible leader Stops enable strict continuation before policy evaluation and interject via `_x.ai/interject`. The 32-attempt cap counts only successfully delivered interjections in one consecutive no-progress series for the same block; transport/protocol failures do not consume it, and material progress, a new block, or a clean Stop resets it. User interrupts are never overridden; transport/protocol failures remain fail-open; `RECONC_GROK_STEER=0` disables steering. `reconc doctor --deep` requires protocol version 1 plus a recognized `_x.ai/interject` response before reporting `Grok leader steering` as active.

The registry assigns 5-second observation/session timeouts, 10-second pre-tool/permission timeouts, and a 30-second Stop timeout. Claude/Codex/Devin/Antigravity/Grok generators emit those host budgets; OpenCode/Kilo Code adapters enforce them internally, kill slow subprocesses, cap combined output at 8 KiB, and never embed a versioned release filename. Generated Claude/Codex/Cursor/Devin/Antigravity/Grok configs do not spawn PreToolUse for read-only matchers; read evidence remains in PostToolUse while pre-execution hooks stay focused on write/shell/apply_patch policy checks. Shell-command runtimes first exec `./tools/reconc/bin/hook` directly when their cwd is already the repo root, and only fall back to `git rev-parse` plus `RECONC_HOOK_REPO_RESOLVED=1` when needed. The agent-hooks audit rejects git-first launchers, Claude/Devin shell/git launchers, project-specific OpenCode/Kilo Code state logic, version-pinned OpenCode/Kilo Code binaries, stale 120-second Antigravity timeouts, and wrapper configs that omit the direct-wrapper fast path. The wrapper trusts the resolved marker or an already-valid repo-local wrapper/dist path, normalizes only direct/manual calls, and execs the first available repo-local Reconc binary. The Go hook runtime lowers observation-only hook priority on Unix for post/after/session-end events; PreToolUse, permission and Stop keep normal priority. The final `exec` keeps hook process trees shallow and avoids idle parent shells where the host runtime allows it.

The wrapper resolves binaries without pinning a Reconc release number. It
tries this exact order:

1. Development binary `.build/bin/reconc`.
2. Development/self-host binary `reconc` at repo root.
3. Stable `tools/reconc/dist/reconc-<os>-<arch>[.exe]`.
4. Exactly one compatible versioned artifact under `tools/reconc/dist/`.
5. The same stable-then-unambiguous lookup under root `dist/`.
6. `reconc` on PATH.

More than one compatible versioned artifact in a searched directory is an
ambiguity error. Install the stable name or retain exactly one versioned
fallback; never select a release by directory order. The first two candidates
avoid OS/architecture subprocess probes on development and self-hosting paths.

Do not require a global `reconc` install. PATH fallback is only a last fallback. POSIX routes in generated JSON hook configs must not inline binary fallback loops; they call `tools/reconc/bin/hook` and let the wrapper own binary selection. PreToolUse, permission and Stop hooks remain hard/interactive priority; only observation hooks are lowered.

On native Windows, generated shell hook routes and `.sh` or extensionless policy
scripts require `sh` on `PATH`; Git for Windows supplies it. Native `.exe` and
`.com` policy scripts execute directly. Do not invent a per-project wrapper variant.

### Step 7a: Assert TASK-scoped claims

Run the task-claim helper from the target repository root through its owning
nested Go module. The command name comes first, followed by the optional
validated TASK override:

```sh
go -C tools/reconc/harness/<project-name> run ./utils/task-claim show
go -C tools/reconc/harness/<project-name> run ./utils/task-claim assert --task TASK-0001-Bootstrap-Reconc
```

The helper walks upward from the harness module and accepts a root only when
both `docs/tasks.md` and the project claim bindings exist. Do not use the
root-module form `go run ./tools/reconc/harness/.../utils/task-claim`; it cannot
cross the nested Go module boundary.

### Step 7b: Activate source-controlled git hooks

The repo-root scaffold ships `.githooks/pre-commit`. This is the source-controlled twin of the hook that `reconc hook install git-pre-commit` would write to `.git/hooks/pre-commit`. It runs `reconc ci --staged` so that commits made via Bash (not just agent-runtime hooks) are also gated by the compiled policy lockfile — this closes the gap where an agent could `git commit` before the agent-runtime Stop hook fires.

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

Use the repo-local Reconc binary candidate that exists on the host.

Required checks:

1. `reconc bootstrap verify --plan .reconc/bootstrap-plan.json --json` when the transactional profile was used.
2. `tools/reconc/dist/<local-reconc-binary> hook status . --json`
3. `tools/reconc/dist/<local-reconc-binary> session-briefing . --json`
4. `cd tools/reconc/harness/<project-name> && go test ./...`
5. `tools/reconc/harness/<project-name>/audits/run-workflow-audit all`
6. Selected stack build/test commands:
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
- The reviewed bootstrap plan matches the applied profile, packs, hooks, binary checksum, and target platform.
- `reconc bootstrap verify --plan ... --json` passes and no unresolved candidate file remains.
- No source-specific product, internal-binary, UI, or local-machine text remains in generic runtime or workflow files.
- `.reconc.yml` points to `tools/reconc/harness/<project-name>/...`.
- `stack-config.yaml` matches the selected stack.
- Every selected policy pack matches real stack evidence; no recommendation was silently auto-applied.
- Hooks prefer development/self-host binaries without platform probes, then local dist binaries on macOS/Linux/Windows, before PATH.
- `repo-root-scaffold/` hook artifacts were synced with `reconc hook sync-scaffold` from the local generator; no hook artifact was edited by hand or copied from a source-specific harness.
- POSIX hook routes call `tools/reconc/bin/hook` first and retain local-dist/PATH fallback; native Windows shell routes have `sh` on `PATH`, and native `.exe`/`.com` policy scripts execute directly.
- `hook status . --json` reports every selected platform as `configured`; no platform is degraded, shadowed, unsupported, or accidentally left only installed.
- OpenCode and Kilo Code plugins contain no project-specific run state or prompts; Antigravity contains no blanket 120-second timeout.
- When Grok is selected, `reconc doctor --deep` proves project trust, project-owned inspect metadata, and every exact native Grok route; strict continuation uses `reconc grok` only after generator-exact hook/wrapper preflight, or cross-platform leader-mode TUI steering when a compatible Grok leader is running (the `Grok leader steering` doctor row verifies protocol version 1 and `_x.ai/interject`).
- Cursor/Windsurf/Codeium/VS Code indexing excludes are installed as local-tool performance controls only, not Git ignores.
- `AGENTS.md` contains the workflow excerpt and any user-approved stack-specific style rules.
- `.gitignore` contains Reconc runtime ignores and relevant dual-layout build/dependency ignores.
- Repo-local binaries under `tools/reconc/dist/` are ignored while `tools/reconc/bin/hook` remains source-controlled.
- `docs/tasks.md` and the active TASK detail file pass task-state audit.
- `session-briefing . --json` returns a supported `format_version`, current
  TASK/policy delta, and repository-run state without a redundant status call.
- `run-workflow-audit all` passes, or any disabled surface is explicitly disabled because it is not part of the selected stack.
