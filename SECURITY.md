# Security Policy

## Supported Versions

Security fixes are applied to the current `main` branch and the latest
published `v0.4.x` release.

## Reporting A Vulnerability

Please report security issues privately before opening a public issue. Use
GitHub private vulnerability reporting if it is enabled for the repository, or
contact the repository owner directly.

Include:

- affected `reconc` version or commit
- operating system and shell
- affected command or hook event
- relevant `.reconc.yml`, `AGENTS.md`, preset, or template input
- lockfile shape when lockfile validation is involved
- hook payload when agent runtime handling is involved
- exact reproduction steps
- expected result and actual result

Do not include live secrets in reports. Redact tokens, private repository URLs,
and proprietary file contents unless they are strictly required for the
reproduction.

## Scope

In scope:

- policy bypasses in `check`, `ci`, `assert`, `can`, hooks, and session gates
- lockfile validation failures that should fail closed
- path traversal or symlink escapes outside the discovered repo root
- secret leakage through default audit/session output
- unsafe execution paths caused by policy, hook, or payload handling
- malformed input that should be rejected but is accepted

Out of scope:

- malicious local users with direct write access to the repository
- intentionally edited generated files after policy compilation
- third-party agent bugs unless `reconc` fails closed incorrectly
- denial-of-service from intentionally huge local repositories outside documented limits
- social engineering, phishing, or issues in unrelated developer tooling

## Security Model

`reconc` is designed to be offline by default. It should not make runtime
network calls while compiling or checking repository policy.

Security-relevant defaults:

- generated policy lockfiles are validated before use
- stale lockfiles and repository-root mismatches fail closed
- hook payloads are treated as untrusted input
- hook payload size, read time, and JSON nesting depth are bounded
- paths are normalized and constrained to the discovered repository root
- payload command strings are matched as data and are not executed
- subprocess execution is limited to policy-authored `require_script` checks
- audit logging is opt-in via `RECONC_AUDIT=1`

## Disclosure

Please give maintainers a reasonable window to investigate and fix confirmed
issues before publishing details. Maintainers should acknowledge valid reports,
confirm the affected surface, ship a fix, and document the user-facing impact.

## No Warranty

`reconc` is provided as-is. There is currently no paid bug bounty program and
no guaranteed response SLA.
