# Contributing to Reconc

Reconc is a small Go CLI with a deliberately strict correctness and trust
boundary. Contributions should keep the binary deterministic, offline by
default, fail-closed on invalid policy or evidence, and honest about what a
repository-local control plane can enforce.

## Before You Start

- Use the [bug form](https://github.com/Christopher-Schulze/reconc/issues/new?template=bug.yml) for reproducible defects.
- Use the [feature form](https://github.com/Christopher-Schulze/reconc/issues/new?template=feature.yml) for substantial behavior changes.
- Report vulnerabilities only through the private route in [SECURITY.md](SECURITY.md).
- Keep pull requests focused on one logical change. Large changes should agree
  on scope in an issue before implementation.

## Development Setup

Requirements:

- Go 1.27
- macOS 13 Ventura or later when developing on macOS
- Git and a POSIX shell
- Bun 1.3.14 only for the executable OpenCode, Kilo Code, Oh My Pi, and Pi adapter tests
- Python 3.13.14 only for the hash-pinned disposable LangChain MCP interoperability test

Build and inspect the CLI:

```bash
git clone https://github.com/Christopher-Schulze/reconc.git
cd reconc
make build
./.build/bin/reconc --help
```

Do not bootstrap Reconc, install generated hooks, or run repository-targeted
Reconc commands against this source repository. `make self-host` creates and
uses isolated temporary repositories for product dogfooding.

## Make A Change

- Put behavior in `internal/`; keep `cmd/reconc/main.go` thin.
- Preserve deterministic ordering, explicit schemas, and stable format versions.
- Treat policy, hook payloads, Git output, and repository paths as untrusted input.
- Update public documentation and generated command surfaces with behavior changes.
- Add tests that fail when the implemented contract is broken. Do not pin prose
  when a semantic invariant can be tested directly.
- Do not include source-planning TASK files; this repository keeps them local
  and gitignored.

## Verify The Result

Use the bounded cached loop while iterating:

```bash
make test-fast
```

Run the complete contributor gate before opening a pull request:

```bash
make test
make test-langchain
make coverage
make vet
make lint
make self-host
make publication-audit
```

`make test-fast` checks formatting and both Go modules with the Go build cache
enabled. `make test` covers the publication audit, root and portable-template
race suites, and release-trust contract. `make test-langchain` builds the Go gateway
and fixture, invokes them through LangChain's official pinned MCP adapter with
no model or service, and denies runtime network access. Its Python environment
is external test infrastructure, never a shipped Reconc dependency.
The test and coverage targets cap package-level parallelism at two by default
to keep an 8 GB development machine responsive. Set `TEST_PARALLELISM` to a
different positive integer when the host has enough capacity. `make coverage`
instruments every package in each module, writes separate root and template
profiles, and reports the measurements for review only. `make cover` records
the same measurements and also writes separate HTML reports. Coverage review
does not replace platform-specific tests. Changes to
release generation, schemas, completion, manpages, installers, or provenance
should also run the relevant release command documented in the
[Makefile](Makefile).

Test setup may cache only immutable, authenticated inputs and must return
detached values to each caller. Repository roots, receipts, policy locks, Git
state, process environment, and runtime evidence remain test-owned because
their identities or mutations are part of the behavior under test. Batch
fixture mutations only when they cross the same production boundary and retain
separate assertions for persistence, rotation, tampering, and cleanup.

## Pull Requests

Describe the problem, the bounded solution, externally visible behavior, and
the evidence used to verify it. Include documentation and security impact even
when the answer is "none." Do not commit generated build output, mutable
`.reconc/` runtime state, credentials, private repository material, or session
data.

Releases are maintainer-owned. Do not move or replace published tags or release
assets; release changes belong to a future version and its explicit release
workflow.

By submitting a contribution, you agree that it may be distributed under the
repository's [MIT License](LICENSE).
