# TASK 161: Build the Go MCP stdio tool gateway

## Why

An advisory `check_action` tool can be skipped by the model. Enforcement needs a
Reconc-owned process boundary that receives every routed `tools/call`, evaluates
it before dispatch, forwards only permitted calls to a downstream MCP server,
and inspects the result before returning it upstream.

The gateway must be a Go component in the single Reconc binary. It will use the
official Go MCP SDK rather than a partial hand-written protocol, but it will wrap
that dependency behind a narrow internal boundary so protocol churn does not
infect the compiler or evaluator.

## Acceptance

- `reconc mcp gateway [repo] --server LABEL (--expect-lock-digest SHA256 |
  --allow-repository-managed-policy) [trusted-context flags] -- COMMAND [ARG...]`
  is registered in the command catalog and starts one local tool-only stdio
  gateway around one operator-selected downstream stdio MCP server.
- All Reconc-owned Action Plane production code is Go. No Python or TypeScript
  SDK, wrapper, helper process, package, or published adapter is added for this
  integration. Existing unrelated generated host-adapter assets are preserved.
- The downstream command, argv, working directory, inherited environment, and
  authority configuration come only from operator launch context after `--` and
  can never be selected by repository policy, tool arguments, or MCP metadata.
- The resolved executable is checked before launch and bound into a server
  fingerprint with executable content identity, canonical argv digest, working
  directory identity, and selected non-secret launch metadata. Raw secrets and
  environment values are never recorded.
- The operator-pinned mode refuses startup and later dispatch on lock-digest
  drift. Repository-managed mode is explicit, lower-provenance, and reported as
  vulnerable to an actor that can modify and refresh repository policy.
- Upstream stdout contains only valid MCP frames. Downstream stderr is captured
  under a hard limit, redacted and classified before any bounded Reconc summary
  reaches stderr, and never copied raw into protocol, ledger, or diagnostics.
- The gateway advertises only the tools capability it can enforce. It preserves
  safe tool names, descriptions, input/output schemas, annotations, icons,
  structured content, errors, pagination, and tool-list changes after strict
  bounds, schema checks, and configured metadata inspection, without claiming
  prompts, resources, sampling, roots, tasks, or transparent general-proxy
  behavior.
- Each exposed tool has a canonical contract digest over its validated name,
  descriptions, input/output schemas, annotations, icons, and safe metadata.
  Calls, caches, approvals, budget reservations, and ledger events record that
  digest; a list change invalidates earlier decisions and receipts before
  another dispatch but cannot reset cumulative budget consumption.
- Upstream arguments remain `json.RawMessage` through the SDK boundary. Reconc
  rejects duplicate keys and validates the discovered input schema itself
  without applying defaults, coercions, remote references, or other semantic
  mutation before forwarding the original canonical values.
- Every routed `tools/call` passes through strict decoding, current policy/lock
  freshness, trusted-context binding, pre-evaluation, cache resampling, budget
  reservation, approval verification when required, downstream dispatch,
  settlement, post-result inspection, ledger completion, and bounded response.
- `block` and unresolved `require_approval` never invoke the downstream tool.
  `warn` and `allow` preserve the original canonical arguments exactly.
- Result withholding prevents raw downstream content from reaching the upstream
  client while reporting a bounded structured Reconc reason. Downstream
  side-effect status remains explicit.
- Upstream cancellation, deadlines, progress, input-required approval flow,
  downstream cancellation, EOF, invalid frames, protocol errors, process exit,
  signal handling, and shutdown races propagate through one owned lifecycle.
- Downstream progress text and other logging-like notifications are untrusted
  streaming result data and pass TASK 159 inspection before forwarding; raw or
  unsupported notifications are suppressed or fail according to policy.
- Parallel calls are safe and bounded. Per-call state is isolated while budgets,
  receipt consumption, caches, and ledger appends remain atomic.
- Child stderr flooding, stdout flooding, invalid JSON, oversized arguments or
  results, tool-list churn, duplicate request IDs, downstream hangs, child
  descendants, executable replacement, path/symlink races, and unavailable
  policy state fail according to the frozen matrix without leaks or orphaned
  processes.
- Current MCP protocol `2026-07-28` and legacy `2025-11-25` tool clients are
  covered through the official Go SDK. Unsupported features and capability
  differences produce explicit bounded errors.
- The official Go MCP SDK version is pinned after re-verification. Dependency,
  license, transitive module, vulnerability, SBOM, reproducibility, and binary
  size changes are reviewed before acceptance.
- Documentation states exactly that only tools configured to use the Reconc
  gateway are enforced. Direct downstream access and native LangChain tools are
  not intercepted.
- Unit, integration, end-to-end, protocol-conformance, race, fuzz, process,
  cancellation, cross-platform, dependency, and mutation tests prove the gateway
  cannot bypass the evaluator or leak withheld results.

## Sub-Tasks

- [x] Re-verify the current MCP protocol and official Go SDK signatures,
      supported versions, conformance claims, license, and transitive modules
- [x] Pin the reviewed SDK version and isolate it behind a small internal
      transport interface owned by the gateway package
- [x] Measure module graph, SBOM, license inventory, vulnerability scan, build
      time, and binary-size baseline before and after the dependency
- [x] Register the exact `mcp gateway` usage, flags, output modes, exit codes,
      and unsupported combinations in the command catalog
- [x] Require exactly one policy-authority flag, bind the pinned lock digest into
      every call, and mark repository-managed mode explicitly in status/evidence
- [x] Parse the operator command only after `--`; reject an empty command,
      ambiguous flags, repository-sourced launch fields, and unsafe path state
- [x] Resolve and fingerprint the executable, argv, working directory, and
      selected non-secret launch metadata through the TASK 157 identity owner
      before policy evaluation or launch
- [x] Start and own one downstream stdio MCP child with bounded stderr, clean
      environment policy, redacted stderr summaries, process-group/job
      ownership, and graceful termination
- [x] Expose one upstream stdio MCP server whose stdout is protocol-only and
      whose advertised capabilities are the exact supported tool subset
- [x] Discover downstream tools and preserve bounded tool metadata, schemas,
      annotations, icons, pagination, and list-change notifications
- [x] Treat descriptions, titles, annotations, schema text, and icon/resource
      metadata as untrusted; reject definitions that fail bounds, schema, URI,
      or configured deterministic injection inspection before exposure
- [x] Canonicalize and digest every accepted tool contract; bind its generation
      to decisions and invalidate in-flight calls, caches, approvals, and budget
      reservations on drift without erasing cumulative budget consumption
- [x] Use the official SDK's low-level RawMessage tool handler, then validate
      input schemas locally without defaults, coercion, remote references, or
      loss of duplicate-key evidence
- [x] Reject duplicate, invalid, colliding, oversized, or dynamically unsafe
      tool definitions before exposing them upstream
- [x] Normalize each call into the canonical action request with a Reconc call
      ID and hashed upstream correlation metadata
- [x] Enforce current lock/source identity, trusted context, pre-decision cache,
      budget reservation, approval, and ledger-required policy before dispatch
- [x] Forward permitted arguments without semantic modification and correlate
      downstream response, error, progress, cancellation, and input-required
      state to the exact call
- [x] Inspect downstream progress before upstream forwarding and suppress MCP
      logging or extension notifications outside the advertised enforceable
      capability set
- [x] Apply budget settlement, post-result schema and detector policy, ledger
      finalization, and result withholding before upstream delivery
- [x] Implement complete timeout and cancellation ownership without goroutine,
      pipe, child-process, reservation, or receipt leaks
- [x] Handle downstream EOF, malformed frames, protocol version mismatch, child
      crash, signal, executable identity drift, and unknown outcome explicitly
- [x] Bound concurrent calls, pending approvals, frame bytes, metadata bytes,
      stderr bytes, result bytes, progress events, and shutdown duration
- [x] Build a real Go fake downstream MCP server whose handlers record whether a
      blocked call was ever invoked and emit every supported result/error shape
- [x] Add end-to-end tests for allow, warn, block, approval, budget, cache,
      ledger-required, result withhold, cancellation, timeout, and tool-list
      change flows
- [x] Add adversarial tests for protocol corruption, floods, collisions,
      duplicate IDs, schema abuse, symlink/executable swaps, child descendants,
      stderr leakage, output leakage, and shutdown races
- [x] Run official MCP conformance coverage for current and supported legacy tool
      behavior and record every intentionally unsupported capability
- [x] Add Linux, macOS, and Windows process lifecycle and cross-compilation tests
- [x] Add fuzz and mutation tests proving every call path reaches pre-evaluation
      and every returned path reaches post-result policy before upstream delivery
- [x] Update RFCs, schemas, architecture, documentation, commands, examples,
      status, doctor, assurance, release, SBOM, and publication surfaces
- [x] Re-read every modified file and run focused tests, full race suites, all
      module gates, static analysis, vulnerability checks, and release builds

## Notes

Depends on TASK 154, TASK 155, TASK 157, TASK 158, TASK 159, and TASK 160. TASK
156 supplies policy regression fixtures but is not a transport dependency.

The plan was checked against official Go MCP SDK `v1.7.0` at commit
`bc72835f62eb94d0fb484439f886b6885b075f36` on 2026-08-10. That release supports
MCP `2026-07-28` and legacy `2025-11-25` and requires Go 1.25; Reconc currently
uses Go 1.26. The version must still be re-verified at implementation start.

Implementation-start verification on 2026-08-11 confirmed the signed stable
`v1.7.0` tag at the same commit, module checksum
`h1:yqjY2dsbKAC0LSuWZVBMrHgiG8ukXv6NRo0JiALay44=`, Go 1.25 minimum,
`2026-07-28` and legacy negotiation, official conformance workflows, and the
low-level `ToolHandler` contract with `CallToolParamsRaw.Arguments` as
`json.RawMessage`. The mixed upstream licensing notice is Apache-2.0 for
relicensed/new code, MIT for non-relicensed older contributions, and CC-BY-4.0
for documentation. Reconc will not use the SDK `CommandTransport`: it owns only
the direct child and does not provide Reconc's required process-group/job,
stderr, executable-race, and descendant lifecycle guarantees.

Before pinning, the root graph contained 24 modules and 40 graph edges; the
trimmed dirty development binary was 15,334,242 bytes; a hot `make build` took
3.53 seconds; generated SPDX/CycloneDX inventories were 24,533/15,999 bytes.

After implementation, the root graph contains 33 modules and 70 graph edges;
the trimmed Darwin arm64 binary is 18,369,298 bytes; a measured hot `make build`
took 0.45 seconds; generated SPDX/CycloneDX inventories are 35,811/22,445 bytes.
Two consecutive local `v0.9.6` release builds produced 48 files and the same
`SHA256SUMS` digest,
`cf81d2ccdce3143cb1d9babc7090809d900ddb78490acca3519982530f6ec0ae`.
These measurements record dependency and artifact cost; no publication was
performed.

The implementation audit found and fixed two adjacent correctness defects. The
gateway shutdown lifecycle now gives owned cancellation precedence over a
simultaneous child/session wait result, with a repeated race regression. The
extended Action Ledger contract now has a new v2 schema while the published v1
asset remains byte-identical under its immutable URL, with both revisions in
the release registry.

The enforcement mutation matrix covers direct dispatch, pre-call blocking,
post-result withholding, refused pre-call approval, and refused post-result
approval against a real Go downstream process. Fuzzing covered frame parsing,
correlation canonicalization, and MCP result decoding. Full root and template
race tests, vet, static analysis, coverage, vulnerability scans, self-hosting,
release trust, cross-compilation, and repeated local release builds passed.

One gateway wraps one downstream server. LangChain can configure multiple
Reconc gateway entries for multiple downstream servers. Multiplexing unrelated
servers inside one process would complicate identity, collision, failure, and
authority boundaries without a demonstrated need.

The downstream executable and `RECONC_HOME` must be operator-owned outside the
agent's write authority for the pinned fingerprint, state, and approval claims
to be independent. Reconc detects identity drift but cannot secure a launcher,
binary path, state root, or signing key that the same agent is allowed to alter.

No HTTP listener, remote control plane, transparent arbitrary MCP proxy, or
fallback direct path is in scope.

## Deviations

None.
