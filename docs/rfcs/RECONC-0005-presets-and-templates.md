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
| `python-assurance` | Current Python test evidence plus changed-source hygiene. |
| `rust-assurance` | Current Rust test, format, warning-free Clippy, and changed-source hygiene evidence. |

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

Stack detection may propose manifested packs with specific evidence. It never
selects wildcard packs and never mutates `extends`; pack adoption remains an
explicit reviewed decision.

## Default Bootstrap

`reconc init` and `reconc bootstrap` default to `default` + `agent` unless
the caller provides explicit `--preset` values. This keeps the initial
experience useful without immediately blocking normal development.

## Templates

Bundled templates live under `internal/templates/builtin/`. User
templates live under `$RECONC_HOME/templates/*.yml` and override
builtins with the same name.

Current builtin templates:

- `authority-change-approval`
- `ci-green-before-merge`
- `custom-gate-on-change`
- `docs-follow-code`
- `local-secret-state-read-only`
- `no-generated-writes`
- `tests-follow-source`
- `verified-change`

A rule using `template: <name>` receives the template's fields as
defaults. User-provided fields win. Template expansion happens before
rule validation, so invalid expanded rules fail at compile time.

## Determinism

Preset and template listing must be sorted by name. Embedded assets are
read as UTF-8. Source order is reflected in lockfile digesting, so
changing preset contents or ordering requires recompilation.
