# TASK 164: Complete Action Plane hardening and release readiness

## Why

The Action Plane crosses policy compilation, untrusted JSON, process launch,
MCP framing, concurrency, mutable budgets, approvals, result privacy, audit
retention, external framework compatibility, and release packaging. Green focused
tests are not enough to call that boundary production-ready.

This task performs the final source-first adversarial pass, closes every confirmed
defect, proves all user-facing claims against the built release output, and leaves
one honest release candidate. It does not choose a version, create a tag, push,
or publish unless Christopher separately requests those exact actions. The
normal local TASK commit still follows the repository lifecycle when this task
is genuinely Done.

## Acceptance

- Every Acceptance item and Sub-Task in TASK 153 through TASK 163 and TASK 165 is
  reverified against source, tests, schemas, RFCs, docs, generated assets,
  command registry, fixtures, and the actual release build, not prior completion
  summaries.
- One end-to-end proof covers LangChain client, Reconc Go stdio gateway,
  production compiler and evaluator, trusted context, budget reservation,
  approval, downstream Go MCP tool, result inspection, ledger, and upstream
  delivery or withholding.
- Negative end-to-end proofs show direct/native tool bypass as unenforced,
  blocked calls never reaching downstream, withheld content never reaching the
  model boundary, and malformed or unavailable enforcement never becoming allow.
- MCP current and supported legacy conformance, LangChain consumer proof, Linux,
  macOS, Windows, amd64, arm64 where available, process lifecycle, cancellation,
  signal, and clean-shutdown paths pass.
- All new parsers, schemas, migrations, predicate operators, caches, state files,
  receipts, detector packs, ledgers, exports, protocols, and process boundaries
  have fuzz, race, corruption, overflow, timeout, cancellation, symlink, special
  file, duplicate, stale-state, and mutation coverage proportional to risk.
- Decision monotonicity, trust monotonicity, deterministic compilation,
  deterministic evaluation, cache soundness, atomic budgets, receipt single use,
  ledger privacy, and result non-disclosure have explicit property or adversarial
  tests.
- Benchmarks measure compiler cost, evaluator latency and allocations, cache hit
  and miss, budget transaction, approval verification, result scanning, ledger
  append, tool discovery, and end-to-end gateway overhead. Documented claims use
  measured reproducible results only.
- The official Go MCP SDK and every added dependency pass license review, module
  verification, vulnerability analysis, SBOM inclusion, reproducible build,
  binary-size review, and release provenance.
- `make test`, complete race suites, `make vet`, `make lint`, `make coverage`,
  `make build`, `make self-host`, publication audit, release-trust, harness-pack
  verification, ShellCheck, Staticcheck, `govulncheck`, module verification, and
  all registered fuzz targets pass on the final snapshot.
- The release target produces the actual candidate artifacts, checksums,
  manifest, SBOM, provenance inputs, completions, and documentation; verification
  consumes those exact outputs rather than a parallel fixture.
- Every command, flag, schema, format, protocol version, decision, error, limit,
  default, coverage boundary, privacy statement, framework claim, and example is
  consistent across README, documentation, architecture, commands, RFCs,
  schemas, guides, templates, harness, `--help`, doctor/status, and release docs.
- Documentation never claims native LangChain interception, general MCP proxying,
  independent approval without an external authority boundary, prevention after
  a side effect, permanent immutable logs, semantic LLM detection, certification,
  or features not proven in the shipped binary.
- No Reconc-authored Action Plane production or adapter code outside Go,
  Reconc-owned runtime network call, remote telemetry, hosted dependency, model
  API dependency, secret-bearing test fixture, placeholder, stub, skipped gate,
  always-green test, TODO, or debug residue remains. Existing unrelated
  generated host-adapter assets are preserved. Network behavior of an
  independently selected downstream tool remains outside this claim.
- `docs/tasks.md` and archived task details report the exact final state. Source
  version, tag, release, remote, and artifact identity remain unchanged unless a
  separate explicit release instruction is executed and verified; the required
  local TASK commit is created only after all Done conditions pass.

## Sub-Tasks

- [x] Reconstruct the complete TASK 153-163 plus TASK 165
      requirement-to-source-to-test-to-doc traceability matrix and mark every
      claim with direct evidence
- [x] Re-read every Action Plane production file, test, schema, RFC, user doc,
      generated asset, fixture, command registration, and release enumeration
- [x] Run an independent architecture pass for duplicated SSOTs, layer leaks,
      circular dependencies, over-abstraction, unowned state, and bypass paths
- [x] Run an independent security pass over trust boundaries, executable launch,
      JSON and schema handling, path identity, secrets, approvals, budgets,
      result containment, logs, and supply chain
- [x] Run an independent correctness pass over decision precedence, predicate
      semantics, migration, determinism, cache identity, state machines,
      cancellation, partial failure, and recovery
- [x] Run an independent concurrency and process pass over parallel calls,
      cross-process locks, reservations, receipt consumption, ledger append,
      child ownership, signals, EOF, and shutdown
- [x] Run an independent privacy pass that seeds unique synthetic secrets and
      personal-data markers through arguments, context, stderr, results,
      progress, tool metadata, exceptions, traces, caches, state, logs, exports,
      reports, and artifacts
- [x] Run an independent false-green pass that mutates or disables each critical
      branch and proves the intended focused and end-to-end tests fail
- [x] Run current and legacy MCP conformance plus real LangChain consumer flows
      for allow, warn, block, approval, budget, cancellation, error, structured
      output, and withholding
- [x] Prove native/direct bypass routes are reported as unenforced and never
      silently included in coverage metrics
- [x] Fuzz every newly reachable parser and protocol boundary under registered,
      reproducible commands and review all crashes, hangs, and resource growth
- [x] Run complete race suites and high-contention multi-process stress tests for
      budgets, approvals, ledgers, caches, gateway calls, and shutdown
- [x] Test corrupt, truncated, oversized, stale, future-version, symlinked, FIFO,
      device, permission-denied, disk-full, and rapidly replaced files at every
      new filesystem boundary
- [x] Test downstream executable replacement, argv ambiguity, stderr/stdout
      floods, malformed frames, duplicate IDs, tool churn, child crashes,
      descendants, hangs, cancellation races, and result leaks
- [x] Benchmark every named hot path from clean reproducible builds and set only
      evidence-backed regression thresholds with documented hardware context
- [x] Audit official Go MCP SDK and transitive dependencies for license,
      vulnerabilities, provenance, maintenance, module integrity, binary size,
      and SBOM truth
- [x] Cross-compile and run available platform-specific suites for Linux,
      macOS, Windows, amd64, and arm64 without weakening unsupported paths
- [x] Rebuild scaffold, harness pack, schemas, completions, manpage, release
      assets, checksums, manifest, SBOM, and provenance through their real owners
- [x] Verify the actual release target output through release-trust and
      publication audits with missing, extra, stale, and tampered negative cases
- [x] Reconcile every README, documentation, architecture, command, RFC, schema,
      guide, template, help, status, doctor, example, and limitation statement
- [x] Scan production and tests for stubs, placeholders, TODOs, debug residue,
      skipped tests, direct unbounded IO, raw secret logging, network calls, and
      non-Go Reconc adapter/runtime code
- [x] Re-read every modified file after final formatting and inspect the complete
      diff for unrelated changes, lost content, generated drift, and version
      changes
- [x] Run the complete final gate matrix again on the exact candidate snapshot
      and retain bounded evidence of commands, versions, and outcomes
- [x] Update TASK control truth and archive only after every acceptance item is
      evidenced; then create the normal local TASK commit, but do not push, tag,
      bump version, or publish without the separately required authorization

## Notes

Depends on TASK 153 through TASK 163 and TASK 165. This is a verification and
repair task, not a substitute for incomplete earlier acceptance. Any
foundational contract change discovered here reopens the owning task surface and
propagates through all affected layers before final gates rerun.

Release readiness is not release publication. Reconc's version changes only on
Christopher's exact requested version, and Git remote or GitHub actions require
separate explicit authorization.

Christopher selected source version `v0.9.6` and planned tag
`reconc-v0.9.6` under TASK 165. This resolves version selection only; TASK 164
still must not create the real tag, push, dispatch, or publish without a
separate explicit instruction.

The source-first traceability review linked every predecessor to its current
production owner and executable proof:

| TASK | Production owner | Direct proof surface |
| --- | --- | --- |
| 153 | `docs/rfcs/RECONC-0008-go-only-action-plane.md` | publication contract audit and all downstream TASK acceptance |
| 154 | `internal/compiler`, `internal/action` | compiler, migration, schema, golden, property, and fuzz tests |
| 155 | `internal/action` | evaluator vectors, properties, cache tests, fuzz, and benchmarks |
| 156 | `internal/impactlab`, `internal/actionevidence/scenario.go` | production-policy scenario comparison, corpus completeness, and drift tests |
| 157 | `internal/actionstate` | context, identity, budget, transaction, recovery, multiprocess, race, fuzz, and benchmark tests |
| 158 | `internal/actionapproval`, `internal/actionstate/approval.go` | receipt, registry, replay, expiry, current input-required, legacy form, fuzz, and lifecycle tests |
| 159 | `internal/actioninspect` | schema, content, detector, privacy, corruption, fuzz, and benchmark tests |
| 160 | `internal/actionledger` | chain, lifecycle, recovery, retention, query, privacy, fuzz, race, and append benchmark tests |
| 161 | `internal/mcpgateway` | raw/current/legacy protocol, process, cancellation, result-containment, mutation, lifecycle, fuzz, race, and end-to-end tests |
| 162 | `scripts/tests/langchain-integration.sh` | hash-pinned real LangChain consumer with Go gateway, Go downstream, external Go signer, bypass, cancellation, withholding, and ledger assertions |
| 163 | `internal/actionevidence`, `internal/cli/action_evidence_cmd.go` | strict report/pack parsing, signed mappings, scenario resampling, privacy, schema, fuzz, and CLI end-to-end tests |
| 165 | `internal/schema`, `scripts/release/schema-assets` | registry, immutable-tag byte identity, schema validation, migration, publication, and real release-trust tests |

The final adversarial pass closed strict evidence JSON and canonical-order
gaps, scenario-count ownership, current and legacy signed approval coverage,
server-request correlation, approved-decision truth, pending-buffer release,
gateway-shutdown exit classification, required-ledger failure terminalization,
repeated tool-schema compilation, and the previously missing project-license
and exact third-party-notice release artifacts. The notice inventory
independently matches the union of all static release-target dependency graphs
and includes the exact Go toolchain notices.

The unchanged final candidate passed the root and portable-template race suites,
all registered fuzz targets, vet, pinned Staticcheck, whole-module coverage,
build, self-host, publication audit, real release-trust, release verification,
harness-pack verification, ShellCheck, both module integrity and tidy checks,
the pinned LangChain integration, vulnerability analysis, high-contention
stress, and deterministic release rebuild comparison. Native macOS execution
and compile-only Darwin, Linux, and Windows amd64/arm64 coverage were available;
non-native operating systems were not falsely represented as locally executed.
No tag, push, workflow dispatch, or release publication occurred.

## Deviations

None.
