# reconc Agent Context

## Project Identity

`reconc` is the active Go implementation of the Repository Control Compiler.
It compiles repository policy into `.reconc/policy.lock.json`, then evaluates
runtime evidence, agent hook events, and git-derived diffs against that
deterministic contract.

This directory is the standalone product repository. Product work stays inside
this root, and docs/comments should not depend on any external source tree.

## Tech Stack

- Language: Go
- Module: `reconc.dev/reconc`
- Runtime dependencies: `github.com/Microsoft/go-winio`,
  `github.com/bmatcuk/doublestar/v4`, `github.com/dlclark/regexp2`,
  `github.com/pelletier/go-toml/v2`,
  `github.com/santhosh-tekuri/jsonschema/v6`, `golang.org/x/sys`,
  `golang.org/x/term`, `golang.org/x/text`, `gopkg.in/yaml.v3`, and
  `mvdan.cc/sh/v3`
- Fast test loop: `make test-fast` (cached root and portable-template tests with bounded package parallelism)
- Complete test gate: `make test` (publication audit, uncached race suites, and release trust)
- Coverage measurement: `make coverage` (whole-module root and portable-template profiles for review evidence)
- Entry point: `cmd/reconc/main.go`

## Build, Test, And Run

```bash
make test-fast
make test
make vet
make lint
make coverage
make build
go run ./cmd/reconc --help
make self-host
make publication-audit
```

Bun `1.3.14` is a test-only dependency for executing the generated OpenCode,
Kilo Code, Oh My Pi, and Pi adapter contracts; the shipped Reconc binary does not
require Bun. `github.com/dlclark/regexp2` supplies ECMAScript matching to
offline policy-authoring schema validation and the independent schema-pattern
test oracle. Production schema validation uses
`github.com/santhosh-tekuri/jsonschema/v6` entirely offline.

## Conventions

- Keep the product as one small Go CLI binary with minimal dependencies.
- Keep JSON artifacts deterministic: sorted keys, stable ordering, explicit
  schema and `format_version` fields.
- Keep global CLI ownership truthful: publish the binary and installation
  receipt under one lock, never claim package-manager ownership, and verify
  changes with `reconc doctor --global`.
- Fail closed on malformed policy, stale lockfiles, schema drift, invalid
  globs, and unsupported rule kinds.
- Do not add runtime network calls.
- Put behavior in internal packages; keep `cmd/reconc/main.go` thin.
- Update tests and user-facing docs with behavior changes.
- Preserve Windows implementation and test definitions and develop them to the
  best available knowledge. Never run automatic or long Windows test suites.
  Only explicitly requested Windows smoke checks may run, with a hard two-minute
  job limit. Linux and macOS own complete validation; Windows never blocks
  releases or starts extended repair loops. Preserve Windows release artifacts.
- Keep the repository self-contained; do not depend on files outside this root.
- Work directly on `main`. Never create, publish, or switch to another branch
  unless Christopher explicitly requests that exact branch. Push repository
  commits only to `origin/main` unless he explicitly directs otherwise.
- Never assign, bump, or predeclare a product release version during development.
  Source code, tests, build defaults, and current documentation must not pin a
  current or future product release number. Development builds identify their
  source commit and development state without claiming a product release.
- Christopher alone decides when a chosen commit becomes a version. Only his
  explicit instruction authorizes creating that product tag or publishing that
  release. Never infer authorization from implementation, tests, commits,
  pushes, task completion, release preparation, or an earlier release. No
  exceptions. Release builds obtain the product version from the exact
  explicitly selected Git tag, never from a version literal in the source.
- If Christopher explicitly says to keep the current version and replace or
  republish that version's release, follow that instruction exactly. Never
  substitute a different version number or refuse solely because another
  versioning policy would normally be preferable. If a technical protection
  blocks the requested replacement, report that blocker and ask Christopher
  how to proceed; do not choose another version.
- Never bootstrap Reconc, compile policy, install generated hooks, or run
  repository-targeted Reconc commands against this product repository. Use the
  isolated temporary repositories created by `make self-host`.

## Version And Release Authority

The working source has no assigned product release version. Git commits
identify development state; an explicitly authorized product tag assigns a
release version to one selected commit. Never reserve or target the next
product release number in source, tests, documentation, or build configuration.

Dependency pins, protocol and format revisions, immutable published schema
identities, and historical release records describe separate technical facts.
Preserve their compatibility meaning; never change them merely to match a
product release number or use them to infer release authorization.

Core tests, race tests, vet, static analysis, CodeQL, and independent artifact
verification must pass before an explicitly authorized publication. Passing
those checks does not authorize a tag or release. Coverage is measured across
each complete Go module, not inferred from package-local percentages.
