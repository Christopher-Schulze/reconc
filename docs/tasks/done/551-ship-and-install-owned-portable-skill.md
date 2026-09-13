# TASK 551: Ship and install owned portable skill

## Why

The portable skill exists only in the source tree. Current release asset inventories and install-cli do not package or install it. A normal installation should include the skill, with a verifiable owner and usable discovery location for the five main tools.

## Acceptance

- The complete canonical skill, including all relative references, is embedded in the binary and available as a deterministic verified release asset from the same source.
- Explicit normal installation through the official installers or install-cli installs the skill by default; a documented opt-out exists.
- The default shared discovery location is qualified for the five tools, with disabled discovery, shadowing, overrides, and refresh/restart requirements reported truthfully.
- A receipt identifies installed file digests and ownership. Existing user-modified or foreign content is preserved and reported as a conflict, not overwritten.
- Repeated installation is idempotent; candidate verification and recovery preserve binary/skill/receipt consistency.
- Downloading an archive or raw binary alone is documented as passive; installation is the action that writes the skill.

## Sub-Tasks

- [x] Finalize skill payload ownership, host discovery matrix, and receipt compatibility.
- [x] Embed and export the canonical skill through the existing release artifact pipeline.
- [x] Add explicit installer skill behavior and safe owned publication.
- [x] Extend diagnostics and ownership-aware lifecycle behavior.
- [x] Verify isolated installation/discovery scenarios and update user-facing docs.

## Technical Plan

1. Canonical content remains `skills/reconc/SKILL.md` and `skills/reconc/references/*.md`. Proposed small Go package co-located under `skills/reconc/` uses go:embed on those exact files and exposes a read-only bundle with sorted per-file digests. This avoids generated hand-maintained copies and illegal parent-directory embed paths. Export only the allowlisted skill content, never Go source or arbitrary extra files.
2. Generate a deterministic skill archive and manifest through `scripts/release/generated-assets.sh` and its existing callers/verifier. Include all references, stable paths/modes/order, and an aggregate digest. Bind it through the existing checksums, release manifest, provenance, and independent verification. The binary's embedded payload and exported archive must match byte-for-byte. No product version literal or tag is assigned.
3. Target one shared `~/.agents/skills/reconc` directory rather than five duplicate installs, subject to exact-version discovery verification. Devin, Cursor, OMP, and DSH document or implement that root; qualify Codex's current environment/host root resolution as well. Support an explicit destination override only through the installation contract, and report precedence conflicts without rewriting other hosts' settings.
4. Extend `internal/usercli/user_cli.go`, `receipt.go`, `diagnostic.go`, `internal/cli/install_cli_cmd.go`, `install.sh`, and `install.ps1` with component-aware installation. CLI flags are `--no-skill`, `--skill-only`, and `--skill-dir PATH`. Reject conflicting modes. Skill-only installation must not reinstall or change binary ownership.
5. Important caller boundary: `ensureCurrentUserCLI` also calls InstallCurrentWithReceipt internally. Pass explicit component intent so an incidental binary repair/bootstrap prerequisite does not silently install a global skill. The default applies to a user's explicit install flow.
6. Extend the existing receipt ownership model, with backward-compatible handling of old binary-only receipts. Store canonical skill path, manifest/digests, and source/bundle identity. Treat an existing unowned directory as unmanaged even if its name matches. Recognize legacy content only through verified provenance or explicit adoption, never by trusting a claimed version string.
7. Stage the complete bundle beside the destination, verify it, and publish under the existing installation lock with recoverable backup/receipt ordering. Reject unsafe symlinks, traversal, case/path collisions, unexpected files, edited owned files, and concurrent destination changes. Do not recursively delete or overwrite foreign content. Reuse binary transaction utilities where they fit instead of creating a generic installer framework.
8. Extend doctor to separate binary readiness, skill installation/currentness, discovery eligibility, and actual host-loaded evidence. Ordinary binary uninstall should preserve skill content unless an explicit owned-skill removal option is selected; that removal must validate digests and preserve modifications.
9. Update `docs/documentation.md`, root README installation guidance, embedded guide, skill install reference, help/completion/manpage, schemas, and release trust. Do not claim normal hook installation or MCP configuration follows from skill installation.

## Verification

Run `go test ./skills/reconc ./internal/usercli ./internal/cli ./scripts/audits/publication ./internal/schema` once the new package exists, then `make test-release-trust`. Use temporary homes and real binaries: fresh/repeated install, raw download, no-skill, skill-only, custom path, read-only home, missing reference, altered file, foreign extra file, symlink, interrupted publish, concurrent install, old receipt, source build, and package-manager-owned binary. Verify discovery through supported host inventory/loading interfaces; file existence is insufficient. Preserve Windows definitions, with no automatic Windows suite.

## Dependencies

TASK 544. Uses host contracts from TASKS 546-550. TASK 552 builds update behavior on this bundle/receipt contract.

## Sources and Evidence

- [Devin skill discovery](https://docs.devin.ai/cli/extensibility/skills/overview), [Cursor skill discovery](https://cursor.com/docs/skills).
- [OMP shared-root provider](https://github.com/can1357/oh-my-pi/blob/ae8ba8b357a9e70bf6281de8c37d7888b6e4b779/packages/coding-agent/src/discovery/agents.ts).
- [DSH filesystem skill provider](https://github.com/deepseek-ai/deepseek-harness/blob/c291e7961a515f6d7af9304e7fd1d257929aef26/packages/skill/skill-filesystem/src/index.ts).
- [Current Codex skill loader](https://github.com/openai/codex/tree/8d3c6cc13d41127faa25eebeac00c48410dfe5c5/codex-rs/ext/skills/src/loader), checked 2026-09-12. Root resolution differs between host and execution environment and still requires exact-version qualification.

## Notes

The current skill is 3,777 bytes plus four relative references. OMP providers may be disabled, DSH may disable default roots or override its agents home, and project/native skills may shadow the shared copy. Installation must not claim to override those choices.

The current Codex documentation confirms `$HOME/.agents/skills` as a user-level discovery root and says skill changes are detected automatically, with restart if a change is not visible ([official Build skills guide](https://learn.chatgpt.com/docs/build-skills)). It also states duplicate names can appear rather than being merged, so a valid shared install cannot claim it won precedence over a repository or plugin copy. The `reconc` skill needs only its existing `SKILL.md` and four linked Markdown references; Go packaging code must not become part of the published skill archive.

The installation receipt's v1 schema is a published immutable contract. A skill-owning receipt needs a new additive format/schema contract with an optional complete skill component; old binary-only receipts remain accepted, but name/path or a matching product version alone never prove ownership. Keep `~/.agents/skills/reconc` as the default target with explicit override, and distinguish a readable installed directory from actual host discovery in diagnostics. The installer scripts already delegate binary publication to `install-cli`, so component installation belongs in that locked transaction. `ensureCurrentUserCLI` is an implicit prerequisite caller and must explicitly select binary-only behavior.

Current primary sources confirm the shared local path in [Devin CLI](https://docs.devin.ai/cli/extensibility/skills/overview), [Cursor](https://cursor.com/docs/skills), the pinned [OMP provider](https://github.com/can1357/oh-my-pi/blob/ae8ba8b357a9e70bf6281de8c37d7888b6e4b779/packages/coding-agent/src/discovery/agents.ts), and the pinned [DSH filesystem provider](https://github.com/deepseek-ai/deepseek-harness/blob/c291e7961a515f6d7af9304e7fd1d257929aef26/packages/skill/skill-filesystem/src/index.ts). Cursor expressly excludes `~/.agents/skills` from its cloud/remote sync, and DSH can disable default roots; the installer must report those limits instead of claiming five-host live availability from file presence.

The new receipt format 2 adds an optional skill component while retaining strict decoding of historical format 1 receipts. Explicit `install-cli` and both official installers install the embedded five-file bundle by default, with opt-out; implicit bootstrap preflight remains binary-only. Skill-only mode requires the running binary to match a ready owned receipt. A binary uninstall preserves skill files without receipt ownership by default; `--remove-skill` verifies all owned files and removes them in the locked transaction. In-process publication and removal failures restore the prior binary, skill, and receipt. A forced process termination at the narrow rename/receipt boundary has not yet been crash-recovery tested; do not claim durable crash recovery from the in-process tests.

Completion evidence (macOS arm64, Go 1.27.1): `make test` passed publication audit, both uncached race suites, reference generation checks, and release trust with the deterministic skill ZIP and manifest. `make vet`, `make lint`, `make self-host`, `sh -n` for changed shell scripts, `git diff --check`, and the skill-creator quick validator passed. An isolated source build installed the embedded skill through the real CLI with a matching receipt; release trust exercised the verified POSIX installer with and without the skill and compared every installed file to source. A staged `references` symlink substitution was rejected without touching foreign content. PowerShell was unavailable on this Mac, and host-loaded discovery remains a separate TASK 555 qualification. No product release was published.

## Deviations

None.
