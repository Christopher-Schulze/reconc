# TASK 162: Prove LangChain and MCP interoperability

## Why

Protocol conformance is necessary but user-facing integration claims also need
proof that LangChain can launch the Go Reconc gateway over stdio, discover its
tools, invoke an allowed call, observe a block without downstream execution, and
receive a contained result. Documentation alone is not an end-to-end test.

Reconc must not become responsible for a native LangChain adapter. LangChain's
official MCP adapter remains the client-side integration; Reconc ships one Go
binary and a configuration example.

## Acceptance

- Documentation gives an exact LangChain MCP configuration that launches
  `reconc mcp gateway` as a stdio server and places the real downstream command
  after `--`, including the required operator-pinned or explicitly
  repository-managed policy-authority mode.
- The example uses LangChain's official MCP adapter and states that its package,
  runtime, and lifecycle remain owned by the consumer, not shipped by Reconc.
- A pinned disposable integration job runs a real supported LangChain release
  against the built Go Reconc binary without adding a Reconc-authored Python or
  TypeScript adapter, package, source module, or release artifact.
- The disposable job proves tool discovery, exact input schema, allowed call,
  blocked call with zero downstream invocation, externally signed legacy form
  approval with exactly one downstream invocation,
  cancellation, downstream error, structured result, result withholding, and
  fresh-client sessions.
- A pure-Go protocol suite independently proves the same flows so core CI and
  local development do not depend on Python, Node, LangChain services, API keys,
  LLM calls, or network access.
- The LangChain proof uses a deterministic fake model or direct tool invocation;
  it never requires a hosted model or sends repository data to a third party.
- Fresh `ClientSession` behavior cannot reset principal, budgets, receipt replay,
  ledger correlation, or policy state. A stateful client session is also tested
  where supported.
- The proof covers supported current and legacy MCP protocol behavior and records
  the exact LangChain, MCP adapter, Go SDK, Reconc binary, and fixture versions.
- Transport or tool errors remain distinguishable from policy block,
  approval-required, budget exhaustion, result withholding, and Reconc internal
  failure in both LangChain and raw MCP results.
- Documentation explicitly states that native LangChain tools, alternate MCP
  configurations pointing directly to the downstream server, and other bypass
  routes are outside Reconc enforcement.
- No product feature depends on LangChain internals, middleware, callback APIs,
  agent state injection, or a specific model provider.
- Release and publication audits fail if the example command, supported protocol
  versions, integration matrix, or limitation language drifts from the actual
  gateway and command registry.

## Sub-Tasks

- [x] Re-verify current official LangChain MCP adapter installation,
      configuration, stateless/stateful session, tool error, and transport error
      behavior from primary documentation
- [x] Define the exact supported integration matrix across LangChain, its MCP
      adapter, MCP protocol, official Go SDK, and Reconc versions
- [x] Add an explicit one-time operator command that initializes the private
      action identity key required by the real gateway launch path
- [x] Add a command-registry-derived LangChain stdio configuration example using
      the built Reconc binary and an isolated fake downstream Go server
- [x] Document operator-bound principal, environment, credential label, approval
      authority, policy authority, repository path, and downstream command
      placement
- [x] Build a pure-Go raw MCP end-to-end suite for discovery, allow, warn, block,
      approval, budget, error, cancellation, structured output, and withholding
- [x] Add a disposable external-consumer job that installs pinned upstream
      LangChain packages and contains no Reconc adapter implementation
- [x] Invoke tools directly or through a deterministic fake model so no hosted
      model, API key, nondeterministic planning, or external data transfer occurs
- [x] Prove a blocked tool never reaches the fake downstream handler
- [x] Prove a withheld result never reaches the upstream LangChain tool result
- [x] Prove fresh client sessions cannot reset budgets, approval replay state,
      principal bindings, or ledger lifecycle
- [x] Prove cancellation and timeouts terminate the downstream call and preserve
      correct budget and ledger terminal state
- [x] Assert exact error categories and safe messages at the LangChain boundary
- [x] Record tool schema, structured content, annotations, tool-list changes,
      progress, and unsupported capability behavior
- [x] Add negative configurations that bypass Reconc and verify documentation and
      doctor/status identify them as unenforced rather than safe
- [x] Add publication tests pinning every example to command metadata and the
      declared support matrix
- [x] Update README, architecture, documentation, commands, RFCs, setup guidance,
      troubleshooting, and security-boundary language
- [x] Add reproducibility records for every external package and fixture used by
      the disposable integration job
- [x] Re-read every modified file and run the pure-Go suite, disposable
      LangChain proof, complete repository gates, and publication audits

## Notes

Depends on TASK 161. The external consumer job is integration proof only and is
not included in Reconc release assets. Reconc-authored production and adapter
code remains entirely Go.

The official LangChain adapter currently supports stdio and converts MCP tools
into LangChain tools. Its client is stateless by default and can use explicit
stateful sessions. These facts were rechecked from primary sources at task start
on 2026-08-12.

Implementation-start verification on 2026-08-12 found
`langchain-mcp-adapters==0.3.2` at source tag commit
`cc0f2843fd5becf2cdb641c910533c3747c3b1ef`. It requires Python 3.10 or newer,
`langchain-core>=1.3.3,<2`, `mcp>=1.24,<2`, and
`typing-extensions>=4.14`. The default `get_tools()` path creates a fresh MCP
session for discovery and another fresh session for each tool call; explicit
`client.session()` plus `load_mcp_tools()` owns one stateful session. MCP tool
errors become LangChain error tool messages by default, while transport,
session, and content-conversion failures raise.

The adapter's strict `mcp<2` dependency means its current supported Python MCP
line negotiates legacy protocol `2025-11-25`, not current protocol
`2026-07-28`. Reconc's pinned Go SDK `v1.7.0` covers both. Therefore the
LangChain consumer proof targets the legacy protocol and the pure-Go suite owns
current-plus-legacy protocol proof; documentation must not claim current
LangChain adapter support for `2026-07-28` until upstream removes that boundary.

The real binary launch audit found that every gateway test created the private
action identity key through Go test helpers, while no public CLI command could
initialize it. `reconc action key init` now owns explicit one-time creation;
repeat initialization fails without changing the existing key.

Implementation added a Go-only downstream fixture, a current-plus-legacy raw
MCP matrix, and an official external LangChain consumer job. The external job
pins every direct and transitive Python distribution by hash, checks Reconc,
Go SDK, adapter, LangChain Core, MCP Python SDK, Python, protocol, and fixture
versions at runtime, invokes no model or service, and denies socket access.

Fresh-process raw tests now prove both duplicate approval-receipt rejection and
cumulative approval/call budgets against one durable operator state. The
external proof additionally checks one run, session, and principal ledger group
across default fresh client calls plus an explicit stateful client session.
Existing gateway lifecycle, catalog, progress, protocol, and failure suites own
tool-list-change, annotations, unsupported-protocol/capability, redacted internal
failure, and conservative cancellation terminal-state proof.

TASK 164 added the missing external-consumer approval proof. The LangChain
callback now receives standard MCP form elicitation, delegates signing to an
external Go fixture process, returns only the signed receipt, and proves the
approved call's complete durable ledger lifecycle. The deterministic test key
is fixture material, not proof of production authority independence.

Two implementation defects were found by the real consumer path and fixed at
their source: an approval rule with `cache: never` incorrectly reported
`rule_never` instead of the approval-pending state required for issuance, and
approval-only budgets needed an explicit regression. Diagnostics now report the
enforcement boundary as explicit routes only, external configuration not
inspected, and bypass routes unenforced; a direct-downstream negative control
proves that distinction is operational, not documentation-only.

The first matrix draft named unreleased Python `3.13.15`. Primary Python release
evidence and an exact managed-runtime execution corrected the pin to released
CPython `3.13.14`; the CI, runtime check, docs, and publication contracts now
share that exact value. The universal hash lock was regenerated independently
and compared byte-for-byte.

## Deviations

None.
