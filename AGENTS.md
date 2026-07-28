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
  `github.com/bmatcuk/doublestar/v4`, `golang.org/x/sys`, `gopkg.in/yaml.v3`,
  and `mvdan.cc/sh/v3`
- Test runner: `make test` (publication audit, root module, portable template module, and release trust)
- Coverage gate: `make coverage` (whole-module root and portable-template profiles with explicit floors)
- Entry point: `cmd/reconc/main.go`

## Build, Test, And Run

```bash
make test
make vet
make lint
make coverage
make build
go run ./cmd/reconc --help
make self-host
make publication-audit
```

Bun `1.3.14` is a test-only dependency for executing the generated OpenCode
and Kilo Code adapter contracts; the shipped Reconc binary does not require
Bun.

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
- Keep the repository self-contained; do not depend on files outside this root.
- Never bootstrap Reconc, compile policy, install generated hooks, or run
  repository-targeted Reconc commands against this product repository. Use the
  isolated temporary repositories created by `make self-host`.

## Current Release State

The current source line is `v0.9.x`; the source version is `v0.9.0`. Core
tests, race tests, vet, static analysis, and release artifact generation must
pass before publication. Coverage is measured across each complete Go module,
not inferred from package-local percentages.
