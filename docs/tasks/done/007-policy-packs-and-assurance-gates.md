# TASK 007: Policy packs and assurance gates

## Why

Golem contains valuable control patterns mixed with Omnimus assumptions. The
standalone product needs configurable, stack-aware assurance packs and gate
contracts, not copied project rules or a permanently growing monolithic
workflow audit.

## Acceptance

- Policy packs compose through explicit capabilities, inputs, evidence, and deterministic conflict rules.
- Generic gates cover repository layout, generated-reference integrity, language boundaries, dependency pins, network/process boundaries, substantive proof, and live build/test truth only when configured and applicable.
- Stack detection proposes packs but never silently locks a repository into guessed policy.
- Every imported Golem pattern is stripped of Omnimus paths, names, baselines, and historical evidence.
- Gates are diff-aware where safe, fail closed where authority matters, and expose exact remediation.
- Positive, negative-control, mutation, bypass, and scale tests prove effectiveness rather than theater.

## Sub-Tasks

- [x] Extract reusable gate contracts from Golem without copying product policy.
- [x] Define composable pack capabilities and configuration schemas.
- [x] Implement the smallest high-value generic gate set.
- [x] Add adversarial effectiveness and bypass proofs.
- [x] Document pack selection, extension, and bootstrap integration.

## Notes

Approved areas: 19 Policy pack architecture; 20 Generic assurance gates.

Read-only Golem extraction identified eight reusable contracts: full-repo
layout authority; generator-check success; changed-file language boundaries;
changed-manifest dependency pins; changed-file network and process guard
markers; bounded substantive-proof manifests; and current successful
build/test evidence. Product paths, Omnimus names, spec-line references,
historical baselines, bespoke TSV control planes, and fixed exemptions do not
transfer.

Design: preset manifests declare format, stack selectors, capability IDs,
capability inputs/evidence/rules, and symmetric pack conflicts. A native
`require_assurance` rule owns typed gate configurations. Repository-layout and
proof authority use full configured scope; source, manifest, network, and
process checks inspect only matching changed files. Every scan has file/byte
budgets and every exemption requires a non-empty rationale. Generated-reference
and live-verification gates accept only current successful command results.
Stack detection may recommend matching packs but `adopt --apply` must not add
them automatically.

Hardening review closed two easy theater paths beyond the downstream source:
comment-only guard markers do not satisfy network/process gates, and measured
proof actuals are recomputed from bounded samples before threshold comparison.
Native runs enforce aggregate file, byte, walk, changed-path, and finding
budgets across the complete gate set.

The 1,000-file language/network/process benchmark improved from 81.44 ms,
19.63 MB, and 162,161 allocations per operation to 31.00 ms, 7.66 MB, and
59,141 allocations by sharing changed-file selection, canonical path results,
and immutable file snapshots across overlapping gates. This also removes
duplicate SSD reads inside one assurance evaluation.

Verification passed: `go test ./...`; `go test -race -count=1 ./...`;
`go vet ./...`; `make lint`; `gofmt -l` clean; `go mod tidy -diff` clean;
host build and `--help`; manifest-bearing `preset list --json`; review-only
`go-assurance` detection through `adopt . --json`; `task validate`;
`task check-done --task 007`; and `doctor --deep` with every row OK. The local
managed pre-commit hook's stale `0.5.0` binary candidate was surgically updated
to the current generated `0.6.0` contract and verified byte-identical plus
executable.

## Deviations

None.
