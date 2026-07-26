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

- Go 1.26
- Git and a POSIX shell
- Bun 1.3.14 only for the executable OpenCode and Kilo Code adapter tests

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

Run the complete contributor gate before opening a pull request:

```bash
make test
make coverage
make vet
make lint
make self-host
make publication-audit
```

`make test` covers the publication audit, root and portable-template race
suites, and release-trust contract. `make coverage` instruments every package
in each module, writes separate root and template profiles, and rejects results
below the committed 80% root and 72% template floors. `make cover` runs the
same gate and also writes separate HTML reports. These are regression floors,
not a claim that unexecuted platform-specific paths are covered. Changes to
release generation, schemas, completion, manpages, installers, or provenance
should also run the relevant release command documented in the
[Makefile](Makefile).

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
