# RECONC-0007: CLI Product Lifecycle

- Status: Frozen
- Target: `reconc 0.9.0`
- Global receipt format: `1`
- Repository receipt: `reconc.repository-install/v1`
- Harness pack: `reconc.harness-pack/v1`
- Repository sync plan: `reconc.repository-sync-plan/v1`

## Status Boundary

This RFC is the frozen `v0.9.0` product contract. The complete global
ownership, diagnostics, update, uninstall, canonical init, embedded harness
pack, transactional repository sync, native installers, schemas, and release
gates ship together. The v0.8.8 release remains
the migration baseline and does not implement this lifecycle.

## Product Rules

1. One globally installed `reconc` command owns interactive use. Repository
   hooks may retain pinned local binaries or wrappers, but they do not replace
   the global installation manager.
2. `reconc init` is the canonical repository onboarding command.
   `bootstrap inspect|profiles|plan|apply|verify|remove` remains the transparent
   lower-level transaction interface.
3. Global binary update and repository synchronization are separate
   transactions. Neither implies that the other succeeded.
4. Installation ownership decides who may update or uninstall the binary.
   Direct and source ownership are explicit and never inferred from path shape.
5. No command prompts. The command and its explicit flags are the
   authorization. Ambiguous state fails with one exact non-interactive next
   command.
6. Daily repository control stays offline. Network access occurs only in
   explicit install or update operations.
7. No background updater, daemon, telemetry, shell-profile edit, implicit
   privilege elevation, or mutable-main download exists.
8. User policy, TASK files, documentation, agent instructions outside managed
   blocks, and unrelated repository content are never silently rewritten.

## Canonical Journey

| Stage | Canonical action | Ownership result |
|---|---|---|
| Install on macOS or Linux | Run the immutable native installer | Direct |
| Install on Windows | Run the immutable PowerShell installer | Direct |
| Install a source build | `PATH/TO/reconc install-cli` | Source |
| Diagnose the global CLI | `reconc doctor --global` | Read-only global truth |
| Onboard a repository | `reconc init .` | Transactional repository receipt |
| Daily use | `session-briefing`, `check`, `next`, `done` | Existing repository contract |
| Update the global CLI | `reconc update` | Verified no-op or owner-authorized transaction |
| Plan repository changes | `reconc repo sync plan .` | Read-only deterministic plan |
| Resolve one reviewed blocker | `reconc repo sync resolve --plan PATH --digest SHA256 --path RELATIVE --strategy STRATEGY` | Exact ownership decision or checksum-pinned binary |
| Apply repository changes | `reconc repo sync apply --plan PATH --digest SHA256` | Exact owned transaction |
| Recover an interrupted sync | `reconc repo sync recover .` | Verified finalize, exact rollback, or refusal |
| Verify repository state | `reconc repo sync verify .` | Read-only verification |
| Uninstall the global CLI | `reconc uninstall` | Owner-authorized removal only |
| Remove repository wiring | `reconc bootstrap remove --plan PATH` and selected `hook uninstall` commands | Existing receipt-owned removal |

## Command Contract

The v0.9 additions and changed canonical entrypoints are:

| Command | Effect |
|---|---|
| `reconc doctor --global [--json] [--output PATH]` | Read-only installation, ownership, receipt, binary identity, PATH, platform, and provenance diagnosis. `--global` cannot be combined with a repository operand or `--deep`. |
| `reconc update [--channel stable\|preview \| --version VERSION] [--allow-downgrade] [--from-dir PATH] [--json]` | Selects and verifies an update, applies it for a direct installation, and returns a verified no-op when already current. The default channel is `stable`; a downgrade requires `--allow-downgrade`; `--from-dir` disables network access. |
| `reconc uninstall [--purge-state] [--json]` | Removes only the globally owned installation. Repository state is never removed. `--purge-state` additionally requires a complete recognized-state inventory before mutation; the private coordination lock remains on one persistent inode, and unknown files fail closed. |
| `reconc init [repo] [--profile existing|minimal|governed|advanced] [--pack NAME ...] [--hook KIND ...] [--no-hooks] [--accept-managed-blocks] [--json] [--output PATH]` | Inspects, selects, plans, applies, and verifies through `internal/bootstrap`. It writes the durable plan and repository receipt only as part of the transaction. |
| `reconc repo sync plan [repo] [--output PATH [--replace-output]] [--json]` | Computes the exact change from the repository receipt to the packs embedded in the running binary. Repository inspection is hermetic and read-only; only explicit `--output` may publish the plan outside the repository transaction. |
| `reconc repo sync apply --plan PATH --digest SHA256 [--json]` | Applies only a saved plan whose canonical digest equals `--digest` and whose preconditions still match. |
| `reconc repo sync resolve --plan PATH --digest SHA256 --path RELATIVE --strategy keep-current\|use-target\|use-binary [--binary PATH --checksum SHA256 --platform OS/ARCH] [--json]` | Resolves one exact non-mutable action. `keep-current` releases Reconc ownership, `use-target` publishes the planned target, and cross-platform `use-binary` requires an exact checksum and platform. |
| `reconc repo sync recover [repo] [--json]` | Recovers the durable sync journal. A complete verified after-image is finalized; exact before/after images are rolled back; an external edit returns `refused` without overwrite. |
| `reconc repo sync verify [repo] [--json]` | Verifies the portable repository receipt, managed artifacts, blocks, hooks, policy lock, and installed pack identities without mutation. |

`init` selection is deterministic:

- A repository with no Reconc control artifact defaults to `minimal`.
- A repository with a valid repository receipt reuses its recorded profile and
  selection.
- A partial, mature, ambiguous, or already governed repository without a
  receipt requires an explicit profile and performs no write.
- With no hook flags, fresh `minimal`, `governed`, and `advanced` initialization
  selects Git pre-commit when `.git/` exists and selects only agent platforms
  evidenced by their repository-local configuration directories.
- `--hook` replaces detected hook selection. `--no-hooks` selects none. The
  two forms are mutually exclusive.
- `--pack` selects policy packs. `advanced` additionally selects the immutable
  public advanced harness pack. Harness packs and policy packs remain distinct
  typed manifest kinds under one common versioned pack envelope.
- `--accept-managed-blocks` retains the existing byte-verified marker-only
  acceptance boundary. No whole-file force path is introduced.

Compatibility behavior:

- Bare `reconc bootstrap [repo]` is rejected. Canonical onboarding is
  `reconc init [repo]`; `bootstrap` accepts only its explicit transactional
  subcommands.
- Legacy `--preset` maps to `--pack` and emits a compatibility warning.
- Legacy `init --force` is rejected because the transactional engine never
  overwrites user-owned content.
- Existing `bootstrap inspect|profiles|plan|apply|verify|remove` names and JSON
  formats remain supported. New work is implemented in their shared engine,
  not in a second init-specific artifact path.
- Removing a compatibility alias requires a future major version.

## Output And Exit Contract

Human output is concise, deterministic, and ends with exactly one `Next:` line.
Non-interactive, redirected, and `NO_COLOR` output contains no ANSI bytes.
Commands never ask for confirmation.

After `--json` is recognized, the command emits exactly one JSON document to
stdout for success, refusal, and operational failure. Syntax errors that prevent
recognition of `--json` remain bounded stderr errors. New v0.9 JSON reports use
this common envelope:

| Field | Type | Rule |
|---|---|---|
| `format_version` | string | Operation-specific versioned identifier. |
| `operation` | string | Exact canonical command operation. |
| `status` | string | Operation-specific closed enum. |
| `changed` | boolean | True only after verified publication or removal. |
| `owner` | string or null | `direct`, `source`, or null when unowned or ambiguous. |
| `current_version` | string | Running version, or empty only when no runnable installation exists. |
| `target_version` | string or null | Selected update or sync target. |
| `channel` | string or null | `stable`, `preview`, `exact`, or `source`. |
| `binary_path` | string or null | Canonical installed binary identity. |
| `receipt_path` | string or null | Canonical receipt identity. |
| `plan_digest` | string or null | Lowercase SHA-256 for plan operations. |
| `checks` | array | Stable ordered typed checks. Never null. |
| `actions` | array | Stable ordered typed actions. Never null. |
| `next_action` | string | One exact next action or terminal success statement. |

Operation-specific schemas close every enum and nested object. Unknown fields
are rejected by readers. Text and JSON derive from the same typed result.

Product-generated exit codes remain:

- `0`: informational success, no-op, or verified mutation
- `1`: input, ownership, trust, manager, network, filesystem, or runtime error
- `2`: blocking repository policy decision

## Installation Ownership

The global receipt lives at
`$RECONC_HOME/install/receipt.json`. Its lock is
`$RECONC_HOME/install/receipt.lock`. The receipt is private, bounded to
64 KiB, strict-decoded, self-digested, and atomically published under the lock
only after the binary transaction and PATH identity pass. It contains no token,
release response, shell profile, command output, or repository path.
The lock inode remains after purge so install, update, uninstall, and global
diagnosis cannot split authority by opening different path identities.
Diagnosis uses a validating shared open that does not create or repair state.

Required receipt fields:

| Field | Type | Rule |
|---|---|---|
| `$schema` | string | Immutable `schemas/v1/installation-receipt.schema.json` URL. |
| `format_version` | string | `1`. |
| `manager` | string | `direct` or `source`. |
| `channel` | string | `stable`, `preview`, `exact`, or `source`. |
| `version` | string | Valid build version verified from the binary. |
| `source_repository` | string | Exactly `Christopher-Schulze/reconc` for release installations and `local-source` for source builds. |
| `release_tag` | string or null | Exact immutable tag for release installations. |
| `artifact_name` | string | Exact release artifact or source binary filename. |
| `artifact_sha256` | string | Lowercase SHA-256 of the installed real file. |
| `binary_path` | string | Canonical absolute installation target. |
| `goos` | string | Verified build target OS. |
| `goarch` | string | Verified build target architecture. |
| `source_digest` | string | Embedded production-source SHA-256, or `unavailable` only for a source-local build without embedded release provenance. |
| `provenance_state` | string | `github-verified`, `embedded-verified`, or `source-local`. |
| `installed_at` | string | UTC RFC3339 timestamp. |
| `receipt_digest` | string | SHA-256 of canonical receipt JSON with this field empty. |

The receipt records claimed ownership but never overrides current filesystem
truth. Global diagnosis independently resolves the running executable, the
PATH executable, target identity, checksum, embedded provenance, manager
metadata, and receipt. A mismatch is `stale`, `shadowed`, `ambiguous`, or
`unowned`, never silently repaired.

Manager authority:

| Owner | Update authority | Uninstall authority | Channel support |
|---|---|---|---|
| `direct` | Reconc direct updater | Reconc direct uninstaller | stable, preview, exact |
| `source` | Source toolchain or a newly built path-qualified `install-cli` | Reconc removes only receipt-owned global bytes | source |

## Channels And Release Selection

- `stable` selects GitHub's latest non-draft, non-prerelease stable release.
- `preview` selects the newest non-draft prerelease.
- `--version VERSION` selects exactly `reconc-vVERSION` and records channel
  `exact`. Stable and prerelease semantic versions are accepted; build metadata
  is rejected in release tags. Numeric prerelease identifiers use unbounded
  digit-length and ordinal comparison across the Go, POSIX, and Windows
  lifecycle implementations.
- A target lower than the running version is a downgrade and requires
  `--allow-downgrade`.
- Channel changes require the new channel flag. Receipt channel is never
  changed merely because a version compares differently.
- Unsupported OS or architecture fails before a download.
- Windows arm64 is unsupported in v0.9.0. The native installer and release
  inventory reject it explicitly.
- Direct release targets are darwin/amd64, darwin/arm64, linux/amd64,
  linux/arm64, and windows/amd64.

Online discovery uses bounded HTTPS requests against the fixed public GitHub
repository. Stable uses the latest-release endpoint, preview uses the bounded
release list, and exact uses the tag endpoint. Drafts are always rejected.
After discovery, every download uses the immutable tag URL and verifies exact
repository, tag, asset name, size, SHA256SUMS entry, embedded build version,
target, and source digest. Native installers and every CLI update require
successful GitHub build-provenance verification before candidate execution or
publication. The online verifier is fixed to `Christopher-Schulze/reconc`, the
release workflow, immutable source tag, GitHub-hosted runner, and candidate
digest; missing or failed verification has no fallback to same-origin
checksums. Any non-zero candidate `install-cli` result fails the outer
transaction, which restores prior bytes or reports the exact recoverable
partial state.

`--from-dir PATH` disables all network access. The directory must contain one
strict release manifest, `SHA256SUMS`, the exact platform artifact, the
selected asset's Sigstore bundle, and `trusted_root.jsonl`.
Traversal, symlinks, extra identity aliases, duplicate entries, or an
incomplete local set fail before staging.

## Repository Ownership

The portable repository receipt is `.reconc/install.lock.json`. It is
committable, path-relative, deterministic, strict, and self-digested. It does
not contain a checkout root, user identity, time, global install path, or
credential. Target repository ignore policy re-includes it beside
`.reconc/policy.lock.json`.

Required repository receipt fields:

| Field | Type | Rule |
|---|---|---|
| `$schema` | string | Immutable `schemas/v1/repository-install.schema.json` URL. |
| `format_version` | string | `1`. |
| `product_version` | string | Product version that produced the receipt. |
| `profile` | string | `existing`, `minimal`, `governed`, or `advanced`. |
| `policy_packs` | array | Ordered names and exact manifest digests. |
| `harness_packs` | array | Ordered names, versions, compatibility ranges, and exact pack digests. |
| `policy_sources` | array | Repository-relative policy inputs owned or observed by the plan. |
| `managed_files` | array | Relative path, mode, SHA-256, component, and ownership. |
| `managed_blocks` | array | Relative path, markers, managed-byte SHA-256, and whole-file precondition. |
| `generated_artifacts` | array | Relative path, generator identity, version, and SHA-256. |
| `user_owned_paths` | array | Explicit preserved paths relevant to migration and sync. |
| `plan_digest` | string | Digest of the applied initialization or sync plan. |
| `generation` | integer | Starts at 1 and increments once per verified changed transaction. |
| `receipt_digest` | string | SHA-256 of canonical receipt JSON with this field empty. |

Private transaction receipts may additionally bind the physical checkout for
rollback and removal. They cannot expand ownership beyond the portable
receipt and exact plan.

## Harness Packs

Harness packs use one strict manifest envelope shared with policy pack
identity. The manifest records kind, name, version, compatible product range,
required capabilities, sorted files, mode, ownership type, per-file SHA-256,
total uncompressed bytes, and pack digest. Paths are unique normalized relative
paths. Absolute paths, traversal, links, special files, unknown fields,
unsupported modes, oversized manifests or files, digest mismatch, and
incompatible product versions fail closed.

The v0.9 binary embeds every pack needed by `minimal`, `governed`, and
`advanced`. The checksummed release inventory also publishes the canonical
advanced public harness pack for inspection and offline use. Embedded bytes
and release-pack bytes must have the same manifest and digest. No runtime Git
checkout or mutable branch is a pack source.

The advanced inventory is public and repository-agnostic. Publication audit
and manifest parity tests reject private names, paths, policies, TASK
conventions, workflows, or product-specific behavior.

## Repository Synchronization

Sync always begins with a plan. Planning removes ambient `GIT_*` routing,
disables system and global config, overrides repository hooks and filesystem
monitors, forbids prompts and optional locks, computes `write-tree` in a
temporary object database with the real object database as a read-only
alternate, and rejects a snapshot that changes during inspection.
Repository-local identity config remains available. Planning renders the
target policy lock in memory, so repository bytes, index, refs, object
database, hooks, and Reconc state remain unchanged. The plan binds canonical
repository identity, current and target product, policy-pack and harness-pack
identities, receipt digest, Git HEAD/index/worktree identities, every file or
managed-block precondition, sorted migrations, candidate paths, blockers, and
the canonical plan digest.

Action states are:

- `unchanged`
- `replace-owned`
- `update-managed-block`
- `create-owned`
- `user-drift`
- `orphaned-legacy`
- `incompatible`
- `manual-review`

Only `replace-owned`, `update-managed-block`, and `create-owned` are mutable,
and only when every saved precondition still matches. Each other action
requires one digest-bound `resolve` decision. `keep-current` preserves the
current bytes and releases Reconc ownership, `use-target` publishes only the
planned bytes, and `use-binary` accepts only the platform path implied by an
exact checksum-pinned binary. An invalid generated policy lock cannot be kept.
Every resolution updates the receipt transactionally and requires a fresh plan.

Apply takes both the plan path and its digest, acquires the repository
transaction lock, revalidates repository identity, Git state, receipt, files,
blocks, and pack bytes, then fsyncs a self-digested before/after journal before
the first owned mutation. A successful transaction verifies the complete
repository before removing the journal. If interrupted, every other repository
transaction refuses to run until `recover` classifies the complete state.
Recovery finalizes an all-after state only after verification, restores mixed
or before states only from exact journaled identities, and reports `refused`
without overwriting an external edit. Empty directories whose post-crash
identity cannot be proven are preserved. Journal size, before-image bytes,
paths, file types, modes, JSON structure, and digests are bounded and strict.

Policy migration runs only when the registered compiler says the current
portable lock requires it. User-authored policy sources are never rewritten.
Same-platform binary ownership advances only to the exact running executable.
A cross-platform receipt remains blocked until `use-binary` supplies the
platform's stable artifact with an exact checksum. Apply and verify check
artifacts, hooks, in-memory policy freshness, repository receipt, policy and
harness pack identities, and binary identity before incrementing the receipt
generation.

## Migration From v0.8.x

| Legacy state | v0.9 treatment |
|---|---|
| Native direct installer | The v0.9 immutable installer verifies and replaces the known direct target, then writes a direct receipt. No old receipt is fabricated. |
| `install-cli` source copy | A path-qualified v0.9 build re-runs `install-cli`, verifies the target and PATH identity, and writes a source receipt. |
| PATH shadow | No mutation occurs until the selected target and bare `reconc` resolve to one verified identity. |
| Bootstrap plan and private receipt | `init` or `repo sync plan` imports them as bounded migration evidence. They do not independently grant new ownership. |
| Repo-local stable or versioned binary | Preserved unless the exact repository plan owns it. Ambiguous compatible binaries remain a blocking conflict. |
| Copied standalone toolkit | Classified as orphaned legacy until the advanced pack manifest proves every owned byte. User changes produce review candidates. |
| Partially initialized repository | Inspection reports exact existing state and requires an explicit profile. No default is guessed and no target is overwritten. |
| Legacy `init --force` workflow | Rejected. The operator reviews candidates or selects marker-only acceptance. |

Migration is redundant-first: new receipts and verified artifacts are
published before an old path is eligible for removal. Missing or ambiguous
ownership never becomes direct ownership merely because a filename is
conventional.

## Locking, Rollback, And Trust

- Global install, update, and uninstall serialize on the global installation
  lock. Repository init, sync, and removal serialize on the repository
  transaction lock. Lock order is global before repository when both are
  required.
- Repository sync apply and resolution fsync a bounded before/after journal
  before mutation. Init, plan, apply, resolve, verify, and removal fail closed
  while that journal is pending; only `repo sync recover` may classify it.
- Network data, manifests, receipts, plans, pack files, and subprocess output
  have explicit byte and item limits. JSON and YAML readers reject unknown
  fields and trailing documents.
- Downloads require HTTPS, bounded redirects that remain HTTPS, immutable tag
  identity, exact checksums, and embedded provenance. Credentials are never
  written to receipts, logs, errors, plans, or repository state.
- Publication stages beside the target, verifies bytes and executable behavior,
  syncs file and parent, and atomically publishes. An existing valid binary or
  receipt remains available until the candidate is fully verified.
- Repository-sourced release assets stage inside the destination filesystem and
  publish by atomic create-only hard link, so a concurrently created name is
  never overwritten between validation and copy.
- Symlinks, Windows reparse points and 8.3 aliases, path swaps, special files,
  cross-repository plans, stale Git snapshots, and concurrent mutation fail
  before owned publication.
- Uninstall never removes repository policy, receipts, hooks, TASKs, docs,
  evidence, or user content. Repository removal remains a separate explicit
  receipt-bound operation.

## Platform Matrix

| Path | macOS amd64 | macOS arm64 | Linux amd64 | Linux arm64 | Windows amd64 | Windows arm64 |
|---|---:|---:|---:|---:|---:|---:|
| Direct release | yes | yes | yes | yes | yes | no |
| Source build | diagnosed, user-owned toolchain | diagnosed, user-owned toolchain | diagnosed, user-owned toolchain | diagnosed, user-owned toolchain | diagnosed, user-owned toolchain | diagnosed, user-owned toolchain |
| Advanced embedded harness | yes | yes | yes | yes | yes | no |

An unsupported cell is an explicit error, never a fallback to another
architecture or installation path.

## External Behavior Basis

Release behavior is anchored to the vendors' current primary contracts:

- GitHub's [release API](https://docs.github.com/rest/releases) defines stable,
  prerelease, tag, and asset discovery. The
  [`gh attestation verify` contract](https://cli.github.com/manual/gh_attestation_verify)
  defines online and bundle-backed provenance verification.

An incompatible external contract change requires a new RFC amendment and
matching implementation; this frozen contract is not silently reinterpreted.

## Freeze Evidence

The frozen contract requires:

1. Every listed command and JSON schema is implemented from one typed contract.
2. Native installers, source installs, and legacy migration expose truthful
   ownership.
3. Pack and repository sync paths pass malicious-manifest, path, rollback,
   concurrency, offline, and cross-platform tests.
4. Current docs, help, completions, man page, schemas, release inventory, and
   SBOM match the runtime.
5. A single release candidate passes full local gates, protected CI, CodeQL,
   installer tests, and multi-repository dogfooding.
