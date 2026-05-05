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
- Runtime dependencies: `gopkg.in/yaml.v3`, `github.com/bmatcuk/doublestar/v4`
- Test runner: `go test`
- Entry point: `cmd/reconc/main.go`

## Build, Test, And Run

```bash
go test ./...
go test -race -count=1 ./...
go vet ./...
go build ./cmd/reconc
go run ./cmd/reconc --help
go run ./cmd/reconc compile .
go run ./cmd/reconc doctor . --deep
```

## Conventions

- Keep the product as one small Go CLI binary with minimal dependencies.
- Keep JSON artifacts deterministic: sorted keys, stable ordering, explicit
  schema and `format_version` fields.
- Fail closed on malformed policy, stale lockfiles, schema drift, invalid
  globs, and unsupported rule kinds.
- Do not add runtime network calls.
- Put behavior in internal packages; keep `cmd/reconc/main.go` thin.
- Update tests and user-facing docs with behavior changes.
- Keep the repository self-contained; do not depend on files outside this root.

## Current Release State

The current public release line is `v0.4.x`. Core tests, race tests, vet,
static analysis, and release artifact generation are expected to pass before
release.
