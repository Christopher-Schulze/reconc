# TASK 001: Lock universal evolution program

## Why

Golem contains the operationally evolved Reconc implementation, but it also
contains a large project-specific Omnimus harness. The standalone product needs
every reusable improvement without importing Golem names, assumptions, paths,
or product-only gates. The approved upgrade must therefore be preserved as a
durable, testable program before implementation begins.

## Acceptance

- Golem remains a read-only reference and all product writes stay inside this repository.
- Every approved upgrade area maps to exactly one owning TASK, with cross-cutting verification reserved for TASK 010.
- Generic Reconc behavior and project-specific policy content are explicitly separated.
- `docs/tasks.md` exposes exactly one active TASK and a complete queue.
- The current documentation identifies the TASK control plane as the durable implementation truth.

## Coverage Contract

| Approved area | Owning TASK |
| --- | --- |
| 1 Release fail-open | 009 |
| 2 Harness CI hole | 009 |
| 3 Read-only contract break | 002 |
| 4 Public trust chain | 009 |
| 5 CLI drift | 002 |
| 6 Complexity concentration | 009 |
| 7 Adapt/merge evolved Golem generically | 002-010 |
| 8 Stop fingerprint regression | 003 |
| 9 Session-specific stop scope | 003 |
| 10 Audit retention regression | 004 |
| 11 Cache mutex serializes cold audits | 003 |
| 12 Task audit scaling | 003 |
| 13 Task lifecycle into core CLI | 006 |
| 14 Transactional bootstrap while preserving tutorial | 008 |
| 15 Hook coverage | 005 |
| 16 Hook contract core/capability registry | 005 |
| 17 Thin OpenCode/Kilo adapters | 005 |
| 18 Hook latency budgets/fail semantics | 005 |
| 19 Policy pack architecture | 007 |
| 20 Generic assurance gates | 007 |
| 21 Token-efficient AI control | 006 |
| 22 Standalone self-hosting | 010 |
| 23 Binary/artifact resolution | 008 |
| 24 Metrics/activation truth | 005 |

Golem engine and adapter changes are reference implementations. Omnimus audit
files are pattern sources only. A gate enters standalone Reconc only when its
contract is repository-agnostic, configurable, bounded, deterministic, and
useful outside Golem.

## Sub-Tasks

- [x] Inventory the standalone product, Golem implementation delta, runtime state, logs, caches, temp artifacts, and release surface.
- [x] Separate reusable engine and adapter improvements from Omnimus-specific policy content.
- [x] Map all approved areas to atomic implementation TASKs.
- [x] Establish the persistent TASK control plane and documentation contract.

## Notes

## Deviations

None.
