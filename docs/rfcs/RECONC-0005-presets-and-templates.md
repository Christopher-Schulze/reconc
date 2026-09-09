# RECONC-0005: Presets And Templates

- Status: Frozen
- Contract: bundled and user policy packs/templates

## Presets

Bundled presets live under `internal/presets/packs/` and are embedded
into the binary. User presets live under `$RECONC_HOME/presets/*.yml`
and override bundled presets with the same name.

Current bundled presets:

| Name | Intent |
|---|---|
| `default` | Baseline generated-output protection and manifest/lock coupling. |
| `agent` | Warning-level agent workflow guidance for reads, tests, docs, and changed shipped-source hygiene. |
| `docs-sync` | Public surface changes should update README/docs/changelog. |
| `strict` | Blocking source/test/CI discipline for mature repos. |
| `release` | Release-manifest, checksum, and verification hygiene. |
| `go-assurance` | Current Go test/vet evidence plus changed-file format, network, process, and concurrency boundaries. |
| `bun-assurance` | Exact JSON dependency pins and current Bun test evidence. |
| `npm-assurance` | Exact JSON dependency pins and current evidence for declared npm verification scripts. |
| `pnpm-assurance` | Exact JSON dependency pins and package-scoped evidence for declared pnpm workspace scripts. |
| `yarn-assurance` | Exact JSON dependency pins and current evidence for declared Yarn verification scripts. |
| `typescript-assurance` | Declared typecheck evidence and changed-source hygiene where TypeScript configuration exists. |
| `python-assurance` | Current Python test evidence plus changed-source hygiene. |
| `rust-assurance` | Current Rust test, format, warning-free Clippy, and changed-source hygiene evidence. |
| `shell-assurance` | Current project-native shell verification plus changed-source hygiene. |
| `cpp-assurance` | Current C/C++ build or test evidence plus changed-source hygiene. |
| `java-assurance` | Current Maven or Gradle verification plus changed Java source hygiene. |
| `php-assurance` | Current project-native PHP verification plus changed-source hygiene. |
| `csharp-assurance` | Current .NET test evidence plus changed C# source hygiene. |
| `nextjs-assurance` | Current production build, lint, route-aware type, and changed-source hygiene evidence. |
| `svelte-assurance` | Current production build, Svelte diagnostics, and changed-source hygiene evidence. |
| `zig-assurance` | Current Zig test, format, and changed-source hygiene evidence. |
| `elixir-assurance` | Current Elixir test, format, and changed-source hygiene evidence. |
| `powershell-assurance` | Current Pester, PSScriptAnalyzer, and changed-source hygiene evidence. |

Repos opt in through `.reconc.yml`:

`extends: [default, agent]`

Names may also use `preset:<name>`. Duplicate preset names are
deduplicated after trimming and prefix removal. Unknown preset names
must fail source loading.

Bundled presets carry a `pack` manifest with `format_version`, matching `name`,
summary, stack selectors, capabilities, and explicit conflicts. Every
capability declares non-empty inputs, evidence classes, and real implementing
rule IDs. Selection rejects conflicts deterministically regardless of argument
order. Legacy user presets without manifests remain loadable, but cannot be
stack-recommended and declare no capabilities.

Shared bounded stack detection may propose manifested packs with specific
manifest or source evidence. It never follows symlinks, enters dependency/build
trees, selects wildcard packs, or mutates `extends`; pack adoption remains an
explicit reviewed decision. Framework detection requires declared Next.js or
Svelte/SvelteKit package dependencies rather than inferring from generic
JavaScript source. Assurance packs evaluate native source gates and
recorded command evidence only. They never install or execute a target
toolchain.
Node package-manager detection uses lockfile and `packageManager` evidence,
reports same-boundary conflicts without choosing, and requires only non-empty
scripts present in the inspected manifest. The generic TypeScript pack
conflicts with the Next.js and Svelte packs so framework-specific verification
retains one owner.

## Default Bootstrap

The canonical `reconc init` path includes `default` + `agent` in the profiles
that own policy unless the selected profile defines no policy packs. Explicit
`--pack` values extend that deterministic profile selection; legacy
`--preset` remains only a flag-level compatibility spelling.

## Templates

Bundled templates live under `internal/templates/builtin/`. User
templates live under `$RECONC_HOME/templates/*.yml` and override
builtins with the same name.

Current builtin templates:

- `authority-change-approval`
- `ci-green-before-merge`
- `custom-gate-on-change`
- `docs-follow-code`
- `generated-artifact-consistency`
- `local-secret-state-read-only`
- `no-generated-writes`
- `performance-budget`
- `public-api-compatibility`
- `schema-migration-safety`
- `tests-follow-source`
- `verified-change`

A rule using `template: <name>` receives the template's fields as
defaults. User-provided fields win. Template expansion happens before
rule validation, so invalid expanded rules fail at compile time.

Evidence recipes may include a template-only `recipe` mapping. Its strict
metadata contract contains `input_paths`, repository-relative `cwd`,
`command_identity`, `evidence_identity`, `applicability`, non-empty
`limitations`, `remediation`, `required_rule_fields`, and executable pass/block
`examples`. `required_rule_fields` is limited to supported expanded rule fields
and is enforced after defaults and user overrides are merged. The metadata is
exposed by catalog commands and removed before policy-rule validation;
enforcement remains the existing expanded rule kind.

`tests-follow-source` and `docs-follow-code` accept an owner-aware opt-in by
repeating a `{name}` capture in `paths` and `when_paths`; literal patterns
retain their historical any-companion behavior. The strict pack uses explicit
captures for common Go, TypeScript, and Rust test layouts. Current successful
verification remains a separate `require_assurance` rule when a source-only
change should not require an artificial companion edit.

## Determinism

Preset and template listing must be sorted by name. Embedded assets are
read as UTF-8. Source order is reflected in lockfile digesting, so
changing preset contents or ordering requires recompilation.
