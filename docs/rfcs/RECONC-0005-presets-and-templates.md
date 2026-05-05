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
| `agent` | Warning-level agent workflow guidance for reads, tests, and docs. |
| `docs-sync` | Public surface changes should update README/docs/changelog. |
| `strict` | Blocking source/test/CI discipline for mature repos. |
| `release` | Release-manifest, checksum, and verification hygiene. |

Repos opt in through `.reconc.yml`:

`extends: [default, agent]`

Names may also use `preset:<name>`. Duplicate preset names are
deduplicated after trimming and prefix removal. Unknown preset names
must fail source loading.

## Default Bootstrap

`reconc init` and `reconc setup` default to `default` + `agent` unless
the caller provides explicit `--preset` values. This keeps the initial
experience useful without immediately blocking normal development.

## Templates

Bundled templates live under `internal/templates/builtin/`. User
templates live under `$RECONC_HOME/templates/*.yml` and override
builtins with the same name.

Current builtin templates:

- `tests-follow-source`
- `docs-follow-code`
- `no-generated-writes`
- `ci-green-before-merge`

A rule using `template: <name>` receives the template's fields as
defaults. User-provided fields win. Template expansion happens before
rule validation, so invalid expanded rules fail at compile time.

## Determinism

Preset and template listing must be sorted by name. Embedded assets are
read as UTF-8. Source order is reflected in lockfile digesting, so
changing preset contents or ordering requires recompilation.
