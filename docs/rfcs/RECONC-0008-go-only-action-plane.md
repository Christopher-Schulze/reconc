# RECONC-0008: Go-Only Action Plane

Status: Draft

Contract family: `reconc.action/v1`

Implementation state: proposed and not implemented. No released Reconc binary
implements this RFC, and integrations must not depend on it until its status is
Frozen together with implementation and release evidence.

## Purpose And Boundary

This RFC defines one deterministic Action Plane for tool calls routed through a
Reconc-owned boundary. It extends repository policy without creating a second
policy engine. The same compiled action plan is consumed by evaluation, Impact
Lab, the future MCP gateway, approvals, budgets, result inspection, and the
action ledger.

Reconc remains one Go binary. Reconc-authored production code, adapters, test
servers, and release artifacts for this contract are Go. LangChain uses its own
MCP adapter to launch the Reconc binary over stdio. Reconc does not ship a
Python or TypeScript LangChain adapter.

The enforcement claim is deliberately narrow:

- A tool call is enforced only when the client routes it through the Reconc
  gateway.
- The gateway wraps one operator-selected downstream stdio MCP server.
- Native LangChain tools, direct access to the downstream server, another MCP
  configuration, and any path that bypasses the gateway are unenforced.
- The gateway governs tools only. It is not a transparent proxy for prompts,
  resources, sampling, roots, tasks, HTTP, SSE, or arbitrary MCP extensions.
- Pre-call blocking can prevent a routed downstream invocation. Post-result
  blocking can withhold data from the upstream client, but cannot undo a
  downstream side effect that already happened.

## Current Baseline

The implemented pre-RFC baseline remains unchanged while this document is
Draft:

- top-level `mcp` policy classifies one exact
  `(platform, server_fingerprint, tool)` identity as
  `repository_read`, `repository_write`, `command`, or `external`;
- repository effects extract exact RFC 6901 path or command fields and reuse the
  existing repository evaluator;
- `external` records only bounded route/effect evidence and does not inspect or
  persist arbitrary arguments;
- fingerprinted and unqualified selectors never fall back to one another;
- Cursor's dedicated MCP pre-route and the Claude Code/Codex
  `mcp__<server>__<tool>` namespace can identify an unclassified MCP call,
  while current OpenCode, Kilo, OMP, Pi, and ZCode generic tool surfaces can
  enforce configured exact identities but cannot reliably classify every
  unknown tool as MCP;
- custom runtime manifests and built-in adapters normalize lifecycle envelopes
  into the existing runtime. They do not execute arbitrary manifest code and
  are not an action-policy engine;
- current status and audit data retain bounded redacted identity/effect
  summaries, not server locators, credentials, raw arguments, results, prompts,
  or command bodies.

The Action Plane may supersede the internal representation only after migration
tests prove this baseline behavior remains exact at the TASK 154 boundary.

## Verified External Basis

Contract table AP-T01. Owner: `internal/mcpgateway`. Vectors: `EXT-*`.
Evolution: any supported-version change requires re-verification, updated
conformance evidence, and a revision of this table before release.

| Surface | Verified basis on 2026-08-10 | Draft target |
|---|---|---|
| MCP protocol | Official specification `2026-07-28` | Tools-only current protocol |
| MCP legacy protocol | Official specification `2025-11-25` | Tools-only compatibility where the SDK implements it |
| MCP Go SDK | `github.com/modelcontextprotocol/go-sdk` `v1.7.0`, peeled commit `bc72835f62eb94d0fb484439f886b6885b075f36` | Pin only after TASK 161 dependency review |
| LangChain MCP adapter | Official `langchain-mcp-adapters` documentation, package `0.3.2` | External consumer proof only |
| LangChain | Official documentation and package `1.3.14` | External consumer proof only |

The version snapshot is research evidence, not a current product support claim.
TASK 161 and TASK 162 must re-verify it at implementation time.

Primary references are the
[MCP 2026-07-28 specification](https://modelcontextprotocol.io/specification/2026-07-28),
[official Go SDK](https://github.com/modelcontextprotocol/go-sdk), and
[official LangChain MCP integration](https://docs.langchain.com/oss/python/langchain/mcp).

MCP `2026-07-28` permits a `tools/call` response with
`resultType: input_required`. The retry has a different JSON-RPC ID and echoes
the server's opaque `requestState` plus client-produced `inputResponses`.
Reconc treats both fields as attacker-controlled. The request state is
integrity-protected, and an input response has no approval authority unless it
contains a valid receipt signed by an operator-configured authority.

Tool descriptions, annotations, icons, schemas, client information, capability
claims, `_meta`, and downstream self-description are untrusted protocol data.
They are never authenticated principal, credential, environment, executable,
or policy identity.

## Threat Model

Protected assets are:

- the decision whether a routed tool may execute;
- the exact policy and tool contract used for that decision;
- operator-bound principal, role, environment, credential, run, and session
  labels;
- cumulative budget capacity and reservations;
- approval authority and one-time receipt use;
- raw tool arguments, progress, results, credentials, and downstream stderr;
- ledger integrity and truthful completeness;
- downstream executable, argv, working directory, and selected environment
  boundary;
- upstream protocol integrity and result containment.

The attacker may control model output, tool arguments, JSON-RPC identifiers,
client metadata, MCP self-description, timing, cancellation, concurrency,
retries, request state, input responses, and any downstream result, progress,
notification, stdout, or stderr. The attacker may supply malformed, duplicate,
oversized, stale, ambiguous, or rapidly changing inputs.

The trusted computing base is the exact Reconc binary, the operating-system
process and filesystem primitives it uses, the operator-owned launch
configuration, the selected policy-authority mode, operator-owned state and
key material, configured approval public keys, and protected external CI used
to validate release provenance. The downstream server is not trusted to
describe itself honestly or return safe data.

An actor that can replace the Reconc binary, launcher, downstream executable,
operator state root, approval key, or pinned lock authority is outside this
local boundary. Repository-managed policy explicitly also trusts actors who
can modify and refresh repository policy. Reconc reports that lower provenance
and never calls it operator-pinned protection.

## Non-Goals

This contract does not add a hosted service, daemon, network policy lookup,
remote telemetry, general MCP proxy, HTTP listener, model call, probabilistic
risk score, semantic LLM detector, payload rewriting, cloud dashboard,
distributed budget store, native LangChain middleware, or certification claim.
An operator-selected downstream tool may itself use the network; that behavior
is outside the Reconc runtime-network claim.

## Topology And Lifecycle

The only enforcing topology is:

~~~text
MCP tool client
    -> local Reconc MCP server on stdin/stdout
        -> canonical Action Plane
            -> one operator-selected downstream MCP child on stdin/stdout
~~~

The Reconc process advertises only the tool capability it can enforce. It
discovers and validates downstream tools, exposes the accepted subset
upstream, evaluates every routed call before dispatch, and inspects every
result and progress event before upstream delivery.

One gateway instance owns one downstream child and one repository policy
authority. Multiple downstream servers use multiple gateway entries. Server
multiplexing is excluded because it would merge process identity, collision,
failure, budget, and approval boundaries.

The gateway stdout is protocol-only. Bounded redacted operator diagnostics use
stderr. Raw downstream stderr never reaches upstream protocol, the ledger, or
diagnostics.

## Package Ownership And Dependency Direction

Contract table AP-T02. Owner: `docs/architecture.md` plus dependency tests.
Vectors: `OWN-*`.
Evolution: a package may split only when its old owner is removed in the same
change and the dependency direction below remains acyclic.

| Owner | Responsibility | May depend on |
|---|---|---|
| `internal/action` | Canonical types, strict normalized values, pure evaluator, traces, error taxonomy | Go standard library and immutable compiled values |
| `internal/policy` and `internal/parser` | Authoring DTOs, legacy `mcp` compatibility input, strict YAML validation | `internal/action` |
| `internal/compiler` | Lowering, canonical order, format-5 lock, digests, immutable compiled programs | `internal/policy`, `internal/action` |
| `internal/schema` | Per-artifact schema registry and immutable URL ownership | no runtime policy package |
| `internal/actionstate` | Keyed identities, budgets, replay state, crash-safe local IO | `internal/action` and existing file primitives |
| `internal/actionapproval` | Approval request, canonical receipt, Ed25519 verification, provider boundary | `internal/action`, `internal/actionstate` |
| `internal/actioninspect` | Deterministic input, result, progress, and metadata inspection | `internal/action` |
| `internal/actionledger` | Typed privacy-bounded events, chain, retention, export | `internal/action` and narrow keyed-identity interfaces |
| `internal/actionevidence` | Strict control maps and deterministic local evidence exports | action ledger, action state, approval verification, and Impact Lab read models |
| `internal/mcpgateway` | Official SDK boundary, child lifecycle, orchestration, protocol mapping | action packages, never compiler internals |
| `internal/impactlab` | Format-2 action scenarios and current-candidate comparison | production compiler and `internal/action` |
| `internal/commandmeta` and `internal/cli` | Registry-first command surface and dispatch | public service boundaries only |

`internal/action` performs no filesystem, network, process, clock, MCP SDK,
ledger, approval-provider, or CLI IO. IO packages inject immutable typed
snapshots. `internal/mcpgateway` orchestrates them and may not reimplement
policy semantics.

## Provenance

Contract table AP-T03. Owner: `internal/action`. Vectors: `TRUST-*`.
Evolution: a new provenance class is breaking unless its relative authority,
allowed sources, persistence, and monotonicity are defined here first.

| Class | Rank | Meaning | Examples |
|---|---:|---|---|
| `agent_supplied` | 0 | Value selected by the calling model or client | arguments, input responses, client metadata |
| `adapter_asserted` | 1 | Value normalized or asserted by a host adapter without independent authority | host session labels, MCP client self-description |
| `host_observed` | 2 | Value Reconc directly observes at its process or filesystem boundary | resolved executable bytes, actual cwd identity, tool contract bytes |
| `operator_bound` | 3 | Value supplied through protected launcher or operator state outside repository and agent authority | principal, environment, credential label, expected lock digest |

Provenance is attached to every top-level context value and inherited by its
descendants. Duplicate context keys are invalid. Arguments and client input
responses are always `agent_supplied`. Protocol metadata is at most
`adapter_asserted`. Repository policy cannot create, rename, alias, or upgrade
an operator-bound value.

A lower-rank value may add a more restrictive candidate. It may not satisfy a
condition whose only effect would relax enforcement. An `allow` rule that
depends on context below `host_observed` is rejected. Argument predicates may
trigger `warn`, `block`, or `require_approval`, but cannot turn a blocking
baseline into allow. `path_within` over an argument is lexical evidence only;
filesystem authorization requires a host-observed resolved identity or the
existing repository-effect evaluator.

Principal, role, environment, credential labels, server label, run ID, and
session ID are operator-bound only through gateway launch inputs or
operator-owned configuration. Executable, working-directory, repository, and
computed server identity are host-observed from the operating-system boundary.
Tool names and contracts are host-observed only after downstream discovery and
validation. MCP connection IDs, JSON-RPC IDs, `clientInfo`, client
capabilities, `_meta`, and model-supplied run or session fields are
adapter-asserted or agent-supplied annotations and never replace the
operator-bound values.

Budget, replay, approval, and ledger state keys use the operator-bound run and
session identities, not the lifetime of an MCP connection. A fresh LangChain
`ClientSession` therefore cannot reset or split durable state.

## Policy Authority

Contract table AP-T04. Owner: `internal/mcpgateway`. Vectors: `AUTH-*`.
Evolution: weakening either startup mode or adding an implicit mode is
breaking.

| Mode | Startup requirement | Drift behavior | Truthful evidence language |
|---|---|---|---|
| `operator_pinned` | Exactly one operator-supplied expected lock digest matches a fresh decoded lock | startup and dispatch block on any lock, source, or digest drift | operator-pinned policy |
| `repository_managed` | Explicit `--allow-repository-managed-policy` and a fresh decoded lock | stale state blocks; a valid refreshed repository policy is accepted | repository-managed policy, mutable by repository authority |

Exactly one mode is required. Neither is inferred. Repository policy never
selects the downstream executable, argv, working directory, inherited
environment names or values, operator state root, HMAC key, approval authority,
public key registry, principal, role, environment, credential label, run ID, or
session ID.

The gateway resamples lock bytes, source identity, policy digest, executable
identity, tool contract, and relevant mutable-state identity immediately before
dispatch. Drift before dispatch blocks. Drift first observed after dispatch
marks the downstream outcome honestly and withholds the raw result until a
fresh decision can be established; it never pretends the side effect did not
occur.

## One Canonical Authoring Model

Contract table AP-T05. Owner: `internal/policy`. Vectors: `CFG-*` and
`COMPAT-*`.
Evolution: additive authoring is accepted only with simultaneous parser,
compiler, schema, digest, runtime, explanation, fixture, and documentation
ownership. Unknown fields always fail.

| Section | Purpose | Availability owner |
|---|---|---|
| `actions.tools` | Exact declared tools and existing repository-effect mapping | TASK 154 |
| `actions.rules` | Selectors, bounded conditions, decisions, failure and cache policy | TASK 154 |
| `actions.defaults` | Declared, gateway-unmatched, host-unmatched, error, and cache defaults | TASK 154 |
| `actions.budgets` | Cumulative local limits | TASK 157 |
| `actions.approvals` | Required authority and disclosure policy | TASK 158 |
| `actions.detectors` | Input, result, progress, schema, and content policy | TASK 159 |
| `actions.ledger` | Recording requirement and privacy selection | TASK 160 |

Later-owned fields are frozen here but must be rejected by the parser until
their real enforcement ships. Accepting inert security configuration is
forbidden.

Legacy top-level `mcp.tools` is compatibility input. Each entry lowers into one
`actions.tools` declaration. Legacy `mcp.unclassified` lowers into
`actions.defaults.host_unmatched`: `host` becomes `allow` at the Reconc
boundary and `deny` becomes `block`. It does not affect the gateway default.
No parallel runtime `mcp` plan is serialized.

A policy may contain legacy `mcp` tools and new action rules that select their
lowered declarations. It may not own the same tool declaration through both
`mcp.tools` and `actions.tools`. The compiler rejects overlap.

### Tool Declarations

Contract table AP-T06. Owner: `internal/action`. Vectors: `TOOL-*`.
Evolution: changing identity fields, legacy lowering, or effect semantics
requires a new lock format.

| Field | Type | Contract |
|---|---|---|
| `id` | lower-kebab string | Required, 1 to 64 ASCII bytes, unique |
| `transport` | enum | `host_mcp` or `mcp_stdio` |
| `platform` | string or absent | Required only for `host_mcp`; current built-in or validated `custom:name` |
| `server_label` | lower-kebab string or absent | Required only for `mcp_stdio`; operator binds the runtime value |
| `server_fingerprint` | identity or absent | Exact required fingerprint when configured; legacy host values retain `sha256:<hex>` and stdio values use the keyed identity contract |
| `tool` | string | Exact case-sensitive tool identity |
| `effect.kind` | enum | `repository_read`, `repository_write`, `command`, or `external` |
| `effect.path_fields` | JSON Pointer list | Non-empty only for repository effects |
| `effect.command_field` | JSON Pointer or absent | Required only for command |
| `cost_units` | integer or absent | Added with budgets; exact per-call value from 0 through `2^63-1` |
| `max_result_bytes` | integer or absent | Added with result-byte budgets; 1 through 8 MiB |
| `ledger_name_safe` | Boolean or absent | Added with the ledger; default false |
| `origin` | enum | Canonical lock only: `actions` or `legacy_mcp` |
| `source_identity` | string | Canonical lock only, compiler provenance |

Tool names are valid UTF-8, non-empty, contain no NUL or control character, and
are at most 256 UTF-8 bytes. Gateway-exposed names additionally match the MCP
recommended ASCII tool-name contract `[A-Za-z0-9_.-]{1,128}`. Host
compatibility declarations retain exact safe host identities within the
256-byte hard bound.

The generated legacy ID is `legacy-mcp-` plus the first 48 lowercase
hexadecimal characters of SHA-256 over
`platform NUL server_fingerprint NUL tool`. A collision with any declaration
is a compile error, not a suffixing opportunity.

### Defaults

Contract table AP-T07. Owner: `internal/action`. Vectors: `DEFAULT-*`.
Evolution: changing any default is breaking and requires a new action contract
version and lock format.

| Field | Allowed authoring | Frozen default | Semantics |
|---|---|---|---|
| `declared_tool` | all four decisions | `allow` | Baseline for one valid declared tool |
| `gateway_unmatched` | `block` only in contract v1 | `block` | Undeclared gateway tools never dispatch |
| `host_unmatched` | `allow` or `block` | `allow` | Compatibility baseline; legacy `deny` lowers to `block` |
| `evaluation_error` | `block` only in contract v1 | `block` | Malformed, incomplete, stale, timed-out, or invariant-failed pre-call evaluation |
| `post_error` | `block` only in contract v1 | `block` | Raw result is withheld |
| `progress_error` | `block` only in contract v1 | `block` | Progress event is suppressed |
| `cache` | `exact` or `never` | `exact` | Reuse only under the complete identity contract |

Defaults are always canonical fields in format 5. Omission in authoring inserts
these exact values. A rule candidate merges with the baseline; `allow` cannot
override a stronger baseline or candidate.

### Rules

Contract table AP-T08. Owner: `internal/action`. Vectors: `RULE-*`.
Evolution: new selector, condition, decision, or failure fields require
compiler and evaluator support in the same change.

| Field | Type | Contract |
|---|---|---|
| `id` | lower-kebab string | Required, 1 to 64 ASCII bytes, unique |
| `selector.tool_ids` | string list | Exact declared IDs |
| `selector.transports` | enum list | Exact transport |
| `selector.platforms` | string list | Exact host platform |
| `selector.server_labels` | string list | Exact operator-bound labels |
| `selector.server_fingerprints` | identity list | Exact computed identities |
| `selector.tools` | string list | Exact case-sensitive names |
| `selector.tool_contract_digests` | digest list | Exact host-observed contracts |
| `selector.phases` | enum list | `pre_call`, `post_result`, `progress`, or `observation` |
| `when` | Boolean AST or absent | Absent means true |
| `decision` | enum | `allow`, `warn`, `block`, or `require_approval` |
| `on_indeterminate` | enum | `block` or `require_approval`; default `block` |
| `cache` | enum | `exact` or `never`; default inherited |
| `message` | string or absent | At most 512 UTF-8 bytes; operator text, never a protocol payload template |

An absent selector field is a wildcard. A present empty list is invalid. Lists
contain at most 256 unique values and are canonicalized in bytewise order.
Rule declaration order does not affect enforcement. Stable rule ID orders trace
presentation after decision precedence.

## Canonical Request And Result

Contract table AP-T09. Owner: `internal/action`. Vectors: `REQ-*` and
`PHASE-*`.
Evolution: removing or repurposing a field requires a new contract family.

| Request field | Type | Authority |
|---|---|---|
| `format_version` | exact `1` | Reconc |
| `call_id` | `act_` plus 26 lowercase base32 characters | Reconc CSPRNG |
| `transport` | enum | host-observed |
| `server_label` | label | operator-bound |
| `server_fingerprint` | keyed identity | host-observed from operator launch |
| `tool` | exact string | observed routed request |
| `tool_contract_digest` | SHA-256 identity | host-observed validated tool definition |
| `phase` | enum | gateway lifecycle |
| `repository_identity` | keyed identity | host-observed filesystem identity |
| `policy_digest` | SHA-256 identity | compiled policy |
| `lock_digest` | SHA-256 identity | compiled lock |
| `authority_mode` | enum | operator launch |
| `arguments` | normalized object or absent | agent-supplied, pre-call |
| `result` | normalized result or absent | downstream-supplied, post-result |
| `progress` | normalized value or absent | downstream-supplied, progress |
| `context` | unique keyed values with provenance | mixed, explicit per key |
| `completeness` | typed flags and missing reasons | boundary owners |
| `deadline` | monotonic deadline snapshot | gateway IO boundary |
| `state_version` | opaque exact identity | action-state snapshot |

The pure evaluator receives no raw wall-clock time. IO boundaries convert
trusted time and deadline state into typed snapshots.

The result contains `decision`, stable `reason_code`, ordered matched rule IDs,
bounded trace, completeness, policy and lock identities, cache eligibility and
reason, required approval identity when applicable, budget candidates, and
phase outcome. Gateway lifecycle adds explicit `dispatch_status`
(`not_dispatched`, `dispatched`, `succeeded`, `failed`, or `unknown`) and
`delivery_status` (`not_applicable`, `forwarded`, `withheld`, or
`suppressed`). Missing terminal evidence is `unknown`, never inferred success.

## Strict JSON And Canonical Values

Arguments must be one JSON object. Results and selected context use a closed
value tree with null, Boolean, exact decimal number, UTF-8 string, array, and
object variants. Domain state never uses `map[string]any` or
`interface{}`.

Decoding rules are:

1. Reject invalid UTF-8, invalid escapes, unpaired UTF-16 surrogates, duplicate
   object keys at any depth, trailing values, non-object arguments, and any
   resource-bound violation.
2. Preserve valid strings exactly. Do not normalize Unicode, trim whitespace,
   fold case, apply schema defaults, coerce types, resolve remote references,
   or interpret strings as environment expansion.
3. Sort object keys by unsigned UTF-8 byte order for canonical encoding. Array
   order remains semantic.
4. Parse numbers from `json.Number` grammar into sign, significant decimal
   coefficient, and base-10 exponent. Remove leading coefficient zeros. Remove
   trailing coefficient zeros while incrementing the base-10 exponent once per
   removed zero, so the mathematical value is unchanged. Normalize every zero
   to positive `0`, and reject a numeric lexeme over 1,024 bytes, more than 768
   significant digits, or absolute normalized exponent over 100,000.
5. Canonical number encoding is optional `-` plus the zero-free coefficient,
   followed by lowercase `e` and the signed exponent only when the exponent is
   nonzero. No plus sign or leading exponent zero is emitted. Thus `1.0` and
   `10e-1` both encode as `1`.
6. The gateway forwards the canonical argument bytes produced once at the
   boundary. Every later layer uses those exact bytes. Source spelling and
   object order may normalize, but the JSON value is never defaulted, coerced,
   or otherwise semantically changed.

## Conditions And Boolean Semantics

Contract table AP-T10. Owner: `internal/action`. Vectors: `AST-*`.
Evolution: a new node or result state is breaking until three-valued semantics
and bounds are frozen here.

| Node | Shape | Semantics |
|---|---|---|
| `all` | non-empty node list | false if any false; indeterminate if none false and any indeterminate; otherwise true |
| `any` | non-empty node list | true if any true; indeterminate if none true and any indeterminate; otherwise false |
| `not` | one node | invert true/false; preserve indeterminate |
| `predicate` | field selector, operator, operand | exact operator semantics below |

Exactly one node key is present. Depth is at most 16, nodes per rule at most
1,024, and total compiled nodes at most 262,144. Evaluation repeats those
checks defensively.

A predicate field selector has:

- `source`: `arguments`, `context`, `result`, or `progress`;
- `pointer`: exact RFC 6901 pointer, at most 1,024 UTF-8 bytes;
- `minimum_provenance`: allowed only for `context` and defaulting to the
  minimum safe rank for the rule decision;
- `op` and, except for `exists`, one typed `value`.

The default minimum is `host_observed` for `allow` and
`agent_supplied` for `warn`, `require_approval`, and `block`. An author may
raise but never lower that default. An `allow` rule containing an
`arguments`, `result`, or `progress` predicate is a compile error because
those values cannot relax enforcement.

`pre_call` permits arguments and context. `post_result` permits result and
context. `progress` permits progress and context. `observation` permits context
only. A source-phase mismatch is a compile error. Missing, null where a
non-null value is required, wrong type, wrong container, invalid array index,
or insufficient provenance is indeterminate for every operator except the
defined `exists` leaf-missing case. `exists` returns false for a missing object
member or an out-of-range canonical array index, but remains indeterminate for
a wrong container or syntactically invalid index. In particular, `neq` and
`not_in` never turn missing or wrong-typed data into true.

Pointer resolution follows RFC 6901 exactly. The empty pointer selects the
source root. `~0` decodes to `~` and `~1` to `/`; every other tilde escape is
invalid. Object tokens compare exact UTF-8 bytes. Array tokens are canonical
base-10 non-negative indexes: `0` is valid, a leading zero, sign, whitespace,
overflow, or `-` is invalid for lookup. A syntactically valid pointer whose
container or member does not exist yields the typed missing or wrong-container
state rather than a zero value.

Contract table AP-T11. Owner: `internal/action`. Vectors: `PRED-*`.
Evolution: adding or changing an operator requires a new vector family,
compiler support, runtime support, schema support, and a contract revision.

| Operator | Target | Operand | Exact result |
|---|---|---|---|
| `exists` | any present value including null | absent | true iff the pointer resolves |
| `eq`, `neq` | any normalized value | one normalized value | exact structural equality; decimal numbers compare by mathematical value |
| `in`, `not_in` | non-null scalar | 1 to 256 same-type scalars | exact membership; mixed operand types are invalid |
| `prefix`, `suffix`, `contains` | string | string | case-sensitive UTF-8 sequence operation, no normalization |
| `glob` | string | pattern string | full-string doublestar match using `/` as separator, case-sensitive, precompiled |
| `regex` | string | RE2 pattern string | full-string Go RE2 match, implicitly anchored, precompiled |
| `gt`, `gte`, `lt`, `lte` | number | number | exact arbitrary-precision decimal comparison |
| `url` | string | URL constraint object | normalized absolute hierarchical URL satisfies every constraint |
| `cidr` | string IP | 1 to 256 CIDR strings | parsed address is contained by any canonical prefix |
| `path_within` | string path | path constraint object | normalized lexical path is equal to or below base on a segment boundary |

Glob and regex patterns are at most 4,096 UTF-8 bytes. Runtime data can never
become a pattern. Regex uses Go's bounded RE2 engine, not backtracking.

A URL constraint contains non-empty lowercase ASCII `schemes` and `hosts`,
optional integer `ports`, optional absolute `path_prefixes`, and required
Booleans `allow_query` and `allow_ip_literals`. URL parsing rejects relative or
opaque URLs, userinfo, fragments, empty hosts, non-ASCII host labels that are
not already punycode, zones, malformed percent encoding, control characters,
and ambiguous encoded slash, backslash, NUL, or dot segments. Scheme and DNS
host compare lowercase. A trailing DNS dot is removed. IP literals use
`net/netip` canonical form. Omitted ports normalize to the registered `http`
or `https` default only; other schemes require an explicit port. Path prefixes
compare decoded canonical segments. Query presence is rejected unless
`allow_query` is true; query content never supplies authority.

CIDR parsing rejects zones and non-canonical trailing material, uses
`net/netip`, unmapped IPv4 form, and masked prefixes. Address-family mismatch
is false, not coercion.

A path constraint contains required `style` (`repository`, `posix`, or
`windows`), `base`, and `case_sensitive`. Repository paths use slash separators
and a repository-relative base. POSIX and Windows bases are absolute in their
own lexical grammar. Volume mismatch, NUL, empty segment ambiguity, alternate
data streams, unresolved parent escape, and malformed Windows prefixes are
indeterminate. This operator performs no filesystem IO and follows no link.

## Decisions, Precedence, And Evaluation

Contract table AP-T12. Owner: `internal/action`. Vectors: `DEC-*`.
Evolution: the decision set or ordering is a breaking contract.

| Decision | Pre-call gateway behavior | Post-result behavior |
|---|---|---|
| `allow` | dispatch if all other gates pass | deliver if inspection passes |
| `warn` | record bounded warning, then dispatch | record warning, then deliver |
| `require_approval` | do not dispatch until one exact valid receipt is consumed | withhold until one exact valid receipt is consumed |
| `block` | do not dispatch | withhold raw result |

Precedence is exactly `block > require_approval > warn > allow`. The evaluator:

1. validates request, phase, plan, identities, completeness, and bounds;
2. finds at most one exact tool declaration, rejecting ambiguous ownership;
3. adds the declared, gateway-unmatched, or host-unmatched baseline;
4. adds the existing repository-effect evaluator candidate where applicable;
5. selects rules by exact selector and evaluates conditions;
6. maps indeterminate conditions through `on_indeterminate`;
7. sorts candidates by decision precedence and rule ID;
8. returns the strongest decision and a bounded trace.

Malformed, unknown, timed-out, cancelled, stale, incomplete, or invariant-failed
pre-call evaluation maps to block. It never becomes allow or warning dispatch.
Post-result failure withholds. Progress failure suppresses the event. An
approval satisfies only its exact `require_approval` candidate; it cannot
override block or a different approval requirement.

Post-result policy never authorizes the earlier pre-call. Progress policy never
changes an already committed side-effect status. Observation decisions record
evidence only.

## Error Taxonomy And Trace

Contract table AP-T13. Owner: `internal/action`. Vectors: `ERR-*`.
Evolution: new errors must map to one stable code and one frozen failure action
before use.

| Class | Stable codes |
|---|---|
| Request | `invalid_request`, `duplicate_key`, `invalid_utf8`, `schema_invalid`, `limit_exceeded` |
| Policy | `policy_missing`, `policy_stale`, `lock_mismatch`, `unsupported_phase`, `tool_unclassified`, `tool_contract_stale` |
| Trust | `context_untrusted`, `identity_unavailable`, `condition_indeterminate` |
| State | `budget_exhausted`, `state_unavailable`, `state_corrupt`, `reservation_indeterminate` |
| Approval | `approval_required`, `approval_rejected`, `approval_invalid`, `approval_expired`, `approval_replayed`, `authority_unavailable` |
| Inspection | `inspection_incomplete`, `unsupported_content`, `result_withheld` |
| Recording | `ledger_unavailable`, `ledger_corrupt` |
| Transport | `downstream_unavailable`, `downstream_error`, `downstream_outcome_unknown`, `protocol_error` |
| Lifecycle | `cancelled`, `deadline_exceeded`, `shutdown`, `internal_invariant` |

Trace entries contain rule ID, tool declaration ID, selector match state,
condition state, candidate decision, stable reason code, actual and required
provenance, completeness, and bounded type/category/length summaries. They
contain no raw argument, result, credential, environment value, header,
unrestricted metadata, or matched secret text. A trace has at most 256 entries
and 64 KiB canonical bytes; overflow replaces omitted entries with one explicit
count and makes the full trace incomplete without changing the decision.

## Identity And Cache Contract

Contract table AP-T14. Owner: `internal/actionstate`. Vectors: `ID-*` and
`CACHE-*`. Evolution: removing an identity component or permitting fallback to
an unkeyed persistent digest is breaking.

| Identity | Construction | Persistence |
|---|---|---|
| Policy and lock | existing canonical SHA-256 digest | allowed |
| Tool contract | SHA-256 over canonical validated public tool definition | allowed |
| Executable content | SHA-256 over bounded regular executable bytes | allowed |
| Server | HMAC-SHA-256 over executable digest, canonical argv, cwd identity, sorted inherited environment names, domain-separated HMAC identities of their exact values, and key ID | allowed as keyed identity |
| Repository | HMAC-SHA-256 over canonical filesystem identity and root binding | allowed as keyed identity |
| Argument or result field | domain-separated HMAC-SHA-256 over canonical typed value | allowed only when policy-selected |
| Upstream request | domain-separated HMAC-SHA-256 over protocol and request correlation | optional |
| Call | random `act_` ID | allowed |

Keyed identities use
`hmac-sha256:v1:<key-id>:<64-lowercase-hex>`. Domain labels are unique for
server, repository, argv, environment names, arguments, results, upstream
requests, approvals, budgets, and ledger fields. Missing, unreadable,
wrong-mode, stale, or rotated key material makes the dependent identity
unavailable. Reconc never falls back to plain SHA-256 for low-entropy or
secret-adjacent values.

Safe policy and operator labels use lowercase ASCII
`[a-z0-9](?:[a-z0-9-]{0,62}[a-z0-9])?` and are at most 64 bytes. They are
case-sensitive and never trimmed or normalized. Operator run and session IDs
may use `[A-Za-z0-9._:-]{1,128}`, remain case-sensitive, and are persisted
only through their domain-separated keyed identity. Invalid or duplicate
values are rejected.

The action identity key is exactly 32 random bytes from `crypto/rand`. Its
private format records format version, key ID, creation time, and base64url
key bytes in
`$RECONC_HOME/action/identity-key.json`, an operator-owned regular file with
private permissions. The key ID is the first 32 lowercase hexadecimal
characters of SHA-256 over the random key and is not secret. Key creation,
selection, and rotation are explicit operator operations serialized by
`$RECONC_HOME/action/identity-key.lock` with dependent state. A rotation either
atomically migrates every dependent keyed identity, budget, replay, and ledger
reference or leaves the old generation active and blocks new dependent work.

An in-memory decision cache may reuse a result only when canonical request
bytes, transport, server label and fingerprint, tool, tool contract, phase,
plan and source identities, policy authority, every context value and
provenance, principal, credential labels, repository identity, mutable-state
version, budget reservation state, approval state, evidence taint,
completeness, and cache-policy version are exact. Persistent cache keys use
keyed identities. Approval-required results are non-cacheable until a current,
unconsumed receipt and state snapshot are verified.

Every identity is resampled immediately before use. Missing IDs, uncertain
input identity, dynamic unresolved context, stale state, or any `cache: never`
match makes the decision explicitly non-cacheable.

## Budgets

Contract table AP-T15. Owner: `internal/actionstate`. Vectors: `BUD-*`.
Evolution: a new dimension, reset rule, or capacity-return path requires a new
state format and transition vectors.

Each `actions.budgets` entry has a unique lower-kebab `id`, the same exact
selector shape as a rule, a non-empty `limits` object, `reset`, optional
`window_seconds` only for `fixed_window`, and fixed
`on_exhaustion: block`. Each limit is a positive integer no greater than
`2^63-1`; `concurrent` is additionally bounded by the gateway maximum of four.
Unknown dimensions, zero limits, duplicate IDs, empty selectors, impossible
result reservations, and contradictory reset fields are compile errors.

`cost_units` come only from an exact non-negative per-call value compiled into
the selected tool declaration. A result-byte budget requires that declaration's
`max_result_bytes`. Neither cost nor maximum result size is read from tool
arguments, downstream metadata, or client claims.

| Dimension | Reserved or recorded | Settlement |
|---|---|---|
| `call_count` | one before dispatch | committed once dispatch starts; released only before dispatch |
| `denied_count` | one on final pre-call block | committed with denial event |
| `approval_count` | one before receipt consumption | committed on successful single-use consumption |
| `argument_bytes` | exact canonical bytes before dispatch | committed once dispatch starts |
| `result_bytes` | declared per-tool maximum before dispatch | settle to actual bytes; excess raw result is withheld |
| `cost_units` | exact compiled per-call units before dispatch | committed once dispatch starts |
| `concurrent` | one active slot before dispatch | released only after terminal settlement |
| `rate_window` | the configured dimensions inside an aligned trusted-time window | never reset by client timestamps |

The canonical budget key always contains budget ID, repository identity,
principal, credential labels, server identity, tool declaration ID, and
explicit run, session, and window components or an exact absent sentinel.
Policy digest, executable identity, tool contract, and HMAC key generation are
recorded as governing generation but never create fresh capacity.

Reset is exactly `never`, `operator_run`, `operator_session`, or
`fixed_window`. Run and session resets require corresponding operator-bound
IDs. Fixed windows are UTC epoch-aligned trusted-clock durations from 1 through
86,400 seconds. Clock rollback, discontinuity, missing identity, negative
state, overflow, malformed state, lock timeout, storage exhaustion, and
generation ambiguity block budget-dependent calls.

Counters use saturating unsigned arithmetic. Policy, executable, tool-contract,
or key rotation carries consumption forward conservatively or requires an
explicit operator-authorized atomic migration/reset receipt. There is no
automatic fresh budget.

Reservation happens atomically before dispatch. Pre-dispatch cancellation or
policy drift releases it. Once dispatch starts, call, argument, and cost
capacity is committed even when the outcome is unknown. A crash leaves an
indeterminate reservation that is not returned automatically. Concurrent calls
cannot oversubscribe. Idempotent call IDs prevent double settlement.

The store is private operator state below `RECONC_HOME`, outside repository
redirection. It is bounded, versioned, locked across processes, regular-file
only, no-follow, atomically replaced, file- and directory-synced, and
crash-consistent on Linux, macOS, and Windows.

Budget and approval replay share one transaction domain at
`$RECONC_HOME/projects/<repository-key>/action/state.json` with
`state.lock` and `state-transaction.json`. Its complete file limit is 16 MiB.
This is what makes reservation and receipt consumption atomic for one call.

Status and explanation expose budget ID, safe scope labels, configured limits,
consumed values, live and indeterminate reservations, reset basis, governing
generation, provenance, completeness, and exact remediation. They never expose
credential values, raw keyed inputs, or unrestricted call payloads.

## Approval Contract

Contract table AP-T16. Owner: `internal/actionapproval`. Vectors:
`APPROVAL-*`. Evolution: changing signed fields, canonical bytes, replay
semantics, or accepted algorithms requires a new receipt format.

| Approval request field | Required binding |
|---|---|
| `format_version` | exact `1` |
| `request_id` and `call_id` | random Reconc identities |
| `repository_identity` | exact keyed repository |
| `policy_digest` and `lock_digest` | exact governing policy |
| `server_label`, `server_fingerprint`, `tool_id`, `tool_contract_digest` | exact action selector |
| `phase` | exact phase |
| `principal` and `credential_labels` | exact operator-bound context |
| `selected_argument_identities` | policy-selected domain-separated HMAC values |
| `budget_reservation_id` | required when a budget applies |
| `reason_code` and `rule_ids` | exact decision basis |
| `issued_at` and `expires_at` | trusted UTC times |
| `nonce` | 256-bit CSPRNG value |

An approval receipt repeats every request binding, adds exact decision
`approve`, authority key ID, receipt ID, and signature. Canonical signing bytes
are the canonical JSON receipt without `signature`, prefixed by
`reconc/action-approval-receipt/v1 NUL`. The only signature algorithm is
Ed25519 from the Go standard library.

Request IDs use `apr_` plus 26 lowercase base32 characters. Authority key IDs
use the safe-label contract. Nonces are 32 random bytes encoded as unpadded
base64url. Signatures are the exact 64 Ed25519 bytes encoded as unpadded
base64url. Receipt IDs use `arc_` plus 26 lowercase base32 characters generated
by the authority CSPRNG and are covered by the signature. `expires_at` must be
later than `issued_at` and no more than 120 seconds later.

A receipt is valid for one exact call and one successful atomic consumption.
Verification rejects unknown or revoked keys, invalid signatures,
non-canonical data, field drift, future issuance beyond 30 seconds, expiry,
wrong principal, missing reservation, duplicate nonce, duplicate call ID, and
any decision other than `approve`. Historical verification honors key
activation and revocation intervals and never accepts issuance after
revocation.

Approval public keys and authority policy come only from operator-owned
configuration. Reconc does not describe a same-agent prompt as independent
human approval. A local approver is independent only when its private key and
confirmation UI are outside agent authority.

For MCP `2026-07-28`, a capable client may receive an `input_required` result
with one `elicitation/create` request. The opaque `requestState` contains only
safe identifiers and digests plus expiry and is protected with the action-state
HMAC. A retry must use a different JSON-RPC ID, identical salient arguments,
the exact request state, and a signed receipt in the declared input response.
The client response itself is untrusted. Unsupported or legacy clients receive
a bounded `approval_required` result and no downstream dispatch.

Timeout, cancellation, malformed response, unsupported capability, key
rotation ambiguity, replay-store failure, or shutdown blocks and settles the
budget reservation through the frozen budget state machine.

## Deterministic Content Inspection

Contract table AP-T17. Owner: `internal/actioninspect`. Vectors: `SCAN-*`.
Evolution: new content types, detector packs, or outcomes require fixtures,
false-positive corpus, limits, privacy review, and a contract revision.

| Surface | Supported inspection | Default when incomplete or unsupported |
|---|---|---|
| Selected argument string or JSON | deterministic detector packs before dispatch | block |
| Tool `structuredContent` | strict local schema plus selected JSON detectors | withhold |
| Text content | selected deterministic text detectors | withhold |
| Embedded text resource | URI metadata and selected text detectors | withhold |
| Resource link | bounded URI and metadata checks, no fetch | withhold on unsupported URI or metadata |
| Image, audio, blob, embedded binary resource | type, length, keyed identity only; no OCR or decoding | withhold unless policy explicitly permits type |
| Unknown content or annotation | no trust inference | withhold |
| Progress or logging-like event | inspect before forwarding | suppress |
| Tool error content | inspect as untrusted content | withhold raw unsafe content |

Detector packs are versioned, content-digested, deterministic local data with
stable categories and severities. They may cover configured secret and
credential formats, PII classes, forbidden data, prompt-injection markers,
role or privilege claims, delimiter attacks, and exfiltration markers. They
make no network or model call and store no matched raw text.

Input detector matches add `warn`, `block`, or `require_approval` candidates.
They never add allow. Post-result policy yields deliver, warn-and-deliver,
withhold, or require-schema through the canonical decisions. Content is never
rewritten into apparently safe content. A withheld response contains only
stable categories, rule IDs, safe counts, and correlation ID plus explicit
downstream side-effect status.

Tool annotations are authority only when an operator-bound server fingerprint
explicitly grants that exact annotation class. Otherwise annotations are
display metadata.

Each `actions.detectors` entry has a unique lower-kebab `id`, one exact
detector-pack ID and digest, one or more compatible phases, 1 to 256 RFC 6901
field pointers, an exact restrictive pre-call decision, post-result disposition
`warn`, `withhold`, or `require_schema`, progress disposition `forward` or
`suppress`, and an unsupported-content type allowlist. An empty field list,
unknown pack, unbound pack digest, source-phase mismatch, pre-call allow
decision, raw-content rewrite, or unsupported content type is a compile error.

Raw argument, progress, and result buffers are transient. After canonical
forwarding or a withhold decision, owners drop all references as soon as the
protocol lifecycle permits. Reconc does not claim guaranteed memory erasure in
Go; it guarantees that those bytes are not deliberately persisted, rendered,
cached beyond the exact in-memory call lifecycle, or copied into ledger and
diagnostic types.

## Action Ledger

Contract table AP-T18. Owner: `internal/actionledger`. Vectors: `LEDGER-*`.
Evolution: a new event or field requires a schema revision and privacy field
matrix before emission.

| Event | Minimum lifecycle fact |
|---|---|
| `request_accepted` | bounded request identity and selector |
| `pre_decision` | decision, reasons, rules, completeness |
| `approval_transition` | request/receipt identity, authority, state |
| `budget_transition` | reservation or settlement delta |
| `downstream_dispatch` | dispatch began |
| `downstream_outcome` | succeeded, failed, or unknown |
| `result_inspection` | categories, schema status, completeness |
| `final_delivery` | forwarded, withheld, or suppressed |
| `terminal_failure` | stable failure and known lifecycle state |

Ledger domain types cannot represent raw arguments, results, headers,
credentials, environment values, stderr, prompts, or arbitrary MCP metadata.
Allowed evidence is safe labels, counts, types, categories, rule IDs, policy
and lock digests, keyed selected-field identities, approval and budget IDs,
completeness, timestamps, and latency.

The ledger has its own format-1 schema, path, strict decoder, renderer, and
compatibility contract. It may reuse proven JSONL, locking, rotation, journal,
and hash-chain primitives. It is a tamper-evident retained ledger, not immutable
or permanent storage. Verification reports retained range, archive continuity,
detached head, dropped-history boundary, and event completeness independently.

Recording policy is `required`, `best_effort`, or `off` and defaults to
`required` for the gateway once TASK 160 ships. Required pre-decision recording
failure blocks before dispatch. Best-effort failure is explicit incomplete
evidence and cannot satisfy a control or completeness claim.

`actions.ledger` contains exactly `mode`, `tool_identity`
(`declaration_id`, `exact_name`, or `keyed_name`), and up to 256 selected
argument/result pointer declarations. `declaration_id` is the default.
`exact_name` is valid only when the tool declaration explicitly marks the name
safe for disclosure. Selected values are always keyed identities, never raw.
Retention limits remain product-owned constants and cannot be raised by
repository policy.

The local paths are
`$RECONC_HOME/projects/<repository-key>/action/ledger.jsonl`,
`ledger.jsonl.1`, `ledger.jsonl.2`, `ledger.head.json`,
`ledger.lock`, and `ledger-transaction.json`. The repository key is the
existing safe project-state key and every record still carries the keyed
repository identity. Symlinks, special files, unexpected entries, foreign
journals, and identity drift fail closed. Ledger tail, stats, verify, and
export report evaluated, approved, dispatched, downstream
succeeded/failed/unknown, delivered/withheld, and incomplete terminal calls
without inferring missing events.

## Resource Limits And Memory Admission

Contract table AP-T19. Owner: the package named in the Owner column. Vectors:
`LIMIT-*`. Evolution: a limit change requires boundary tests at minus one,
exact, and plus one and updated aggregate math.

| Resource | Hard limit | Owner |
|---|---:|---|
| Canonical arguments | 8 MiB | `internal/action` |
| Upstream or downstream protocol frame | 10 MiB | `internal/mcpgateway` |
| Canonical tool result | 8 MiB | `internal/actioninspect` |
| Tool name | 256 UTF-8 bytes; gateway also 128 ASCII characters | `internal/action` |
| JSON Pointer | 1,024 UTF-8 bytes | `internal/action` |
| JSON depth | 32 | `internal/action` |
| JSON object keys plus array items | 65,536 per value | `internal/action` |
| One JSON string | 4 MiB | `internal/action` |
| Numeric lexeme/significant digits/exponent | 1,024 bytes / 768 digits / absolute 100,000 | `internal/action` |
| Boolean AST depth/nodes per rule | 16 / 1,024 | `internal/action` |
| Total compiled predicate nodes | 262,144 | `internal/compiler` |
| List operand or selector list | 256 values | `internal/action` |
| Regex or glob source | 4,096 UTF-8 bytes | `internal/compiler` |
| Safe label / run or session ID / rule message | 64 / 128 / 512 bytes | `internal/action` |
| Compiled action rules | 4,096 | `internal/compiler` |
| Declared tools | 512 | `internal/compiler` |
| Tool-list pages / tools per page / total | 64 / 128 / 512 | `internal/mcpgateway` |
| Per-tool / aggregate accepted metadata | 256 KiB / 8 MiB | `internal/mcpgateway` |
| Detector input per call | 8 MiB | `internal/actioninspect` |
| Detector pack / configured detector entries | 8 MiB / 1,024 | `internal/actioninspect` |
| Progress events / one event / aggregate | 128 / 64 KiB / 1 MiB | `internal/mcpgateway` |
| Approval object | 64 KiB | `internal/actionapproval` |
| Ledger record / live file / archives | 64 KiB / 4 MiB / two 4 MiB files | `internal/actionledger` |
| Combined budget and approval-replay state file | 16 MiB | `internal/actionstate` |
| Approval-authority configuration / authority keys | 1 MiB / 256 | `internal/actionapproval` |
| Concurrent calls / pending approvals | 4 / 4 | `internal/mcpgateway` |
| Downstream stderr retained | 256 KiB per child | `internal/mcpgateway` |
| Decision trace | 256 entries and 64 KiB | `internal/action` |
| Compiled action plan | 24 MiB admitted logical bytes | `internal/compiler` |
| Gateway admitted logical memory | 192 MiB | `internal/mcpgateway` |
| Impact corpus / action cases | 64 MiB / 10,000 | `internal/impactlab` |
| Impact delta manifest | 8 MiB | `internal/impactlab` |
| Control-map pack / controls / export | 8 MiB / 4,096 / 32 MiB | `internal/actionevidence` |

The 192 MiB admission calculation is:

- four calls at a 32 MiB maximum phased working set: 128 MiB;
- accepted tool catalog: 8 MiB;
- compiled plan: 24 MiB;
- one bounded state snapshot: 16 MiB;
- ledger, approval, stderr, and protocol coordination: 8 MiB;
- admission margin: 8 MiB.

The cap covers Reconc-owned admitted logical buffers, including decoder
expansion allowances. It is not a promise about Go runtime RSS. Raw request,
normalized value, canonical forwarding bytes, detector workspace, progress,
and result buffers are phase-owned and released before the next incompatible
phase. Allocation that cannot first claim admission fails closed. Streaming
does not bypass byte, item, event, or total limits.

## Timeouts

Contract table AP-T20. Owner: `internal/mcpgateway` unless stated. Vectors:
`TIME-*`. Evolution: a timeout change requires cancellation, leak, and
boundary tests.

| Boundary | Default | Hard maximum | Failure action |
|---|---:|---:|---|
| Gateway startup and child initialize | 15 s | 15 s | startup failure |
| One tool-list page | 5 s | 5 s | startup or refresh failure |
| Complete tool discovery | 30 s | 30 s | startup or refresh failure |
| Policy/source/identity resample | 2 s | 2 s | block |
| Pure pre-call evaluation | 500 ms | 500 ms | block |
| State lock and transaction | 2 s | 2 s | block |
| Required ledger append | 2 s | 2 s | block before dispatch |
| Approval authority | 120 s | 120 s | block |
| Downstream tool call | 60 s | 300 s | cancel, outcome unknown unless authoritative |
| Post-result inspection | 1 s | 1 s | withhold |
| One progress inspection / aggregate | 250 ms / 1 s | 250 ms / 1 s | suppress event |
| Cancellation propagation grace | 2 s | 2 s | terminate child ownership boundary |
| Graceful gateway shutdown | 5 s | 5 s | force owned child termination |
| Child TERM-to-KILL grace | 2 s | 2 s | force kill |
| Final pipe/stderr drain | 2 s | 2 s | bounded incomplete diagnostic |
| Approval clock skew | 30 s | 30 s | reject receipt |

The effective downstream timeout is the minimum of the upstream deadline,
operator-selected timeout, 60-second default when absent, and 300-second hard
maximum. Repository policy cannot increase or select it.

## Failure Matrix

Contract table AP-T21. Owner: `internal/mcpgateway`. Vectors: `FAIL-*`.
Evolution: no boundary may add an allow or delivery path without a new row and
negative end-to-end proof.

| Boundary failure | Before dispatch | After dispatch | Raw result/progress |
|---|---|---|---|
| Invalid or oversized request | block | not applicable | none |
| Missing, stale, malformed, or mismatched policy | block | mark outcome honestly | withhold |
| Tool missing, ambiguous, changed, or contract-stale | block | mark stale generation | withhold |
| Untrusted or incomplete relaxing context | block; `require_approval` only when the compiled indeterminate policy says so | mark incomplete | withhold |
| Evaluator timeout, cancellation, or invariant failure | block | not applicable | none |
| Budget exhausted or state unavailable | block | reservation becomes indeterminate if dispatch began | withhold |
| Approval missing, rejected, invalid, expired, replayed, or unavailable | block | not applicable | none |
| Required ledger unavailable | block | terminal event best effort, outcome honest | withhold |
| Downstream launch or protocol failure | no dispatch or failed dispatch | failed or unknown | safe structured error only |
| Downstream timeout, cancellation, EOF, or crash | no retry | failed or unknown; conservative budget settlement | no raw partial result |
| Result schema or detector failure | not applicable | side effect status unchanged | withhold |
| Unsupported result content | not applicable | side effect status unchanged | withhold unless exact type allowed |
| Progress inspection failure | call policy unchanged | side effect status unchanged | suppress event |
| Gateway shutdown race | block new calls | cancel owned calls, settle conservatively | withhold partial data |

Cancellation before dispatch releases a reservation. Cancellation after dispatch
sends downstream cancellation, prevents new upstream delivery, and records
unknown unless an authoritative terminal outcome arrives. There is no automatic
retry of a side-effecting call.

## MCP Mapping And Safe Results

The gateway validates downstream `tools/list` pages and the complete accepted
catalog before advertising tools. `listChanged` invalidates tool contracts,
decision caches, pending approvals, and uncommitted reservations before another
dispatch. It never resets consumed budget.

The tool-contract digest covers canonical validated name, title, description,
input schema, output schema, annotations, icons, and accepted safe metadata.
Remote schema references, executable metadata, credential hints, unsupported
URIs, duplicate keys, and over-limit definitions are rejected. JSON Schema
validation is local, bounded, and does not apply defaults or coercions.

Gateway policy outcomes use one safe tool-result envelope with:

- `format_version: "1"`;
- `outcome`: `policy_block`, `approval_required`, `budget_exhausted`,
  `result_withheld`, `downstream_error`, `gateway_error`, or `cancelled`;
- stable `reason_code` and bounded message;
- `correlation_id`;
- explicit `dispatch_status` and `delivery_status`;
- no raw blocked arguments, result, stderr, credential, or metadata.

The envelope is distinguishable from a downstream tool error. Legacy protocol
mapping retains the same safe categories in the supported result shape.

## Impact Lab Contract

Contract table AP-T22. Owner: `internal/impactlab`. Vectors: `IMPACT-*`.
Evolution: later action tasks extend format 2, not create parallel corpus
formats.

| Case class | Required exact assertion |
|---|---|
| `action_pre` | decision, reason, ordered rules, phase, cache eligibility, completeness |
| `action_post` | decision, containment, schema, ordered rules, completeness |
| Budget extension | reservation, settlement, exhaustion, state generation |
| Approval extension | request, receipt state, authority, single-use result |
| Detector extension | category, completeness, delivery or withhold |
| Ledger extension | required lifecycle event and privacy-bounded fields |

Impact corpus format 2 binds canonical selector fixtures, arguments or result,
explicit provenance, state snapshot, and expected outcome. Omitted decision
expectations are invalid. Production compiler and evaluator execute every case.
A test-only policy engine is forbidden.

Each action case contains case ID, phase, server label and fingerprint, tool
declaration ID and exact tool name, tool-contract digest, canonical argument or
result fixture, context values with provenance, principal and credential
labels, authority mode, evaluator state identity, completeness declaration,
and exact expected decision, reason, ordered rule IDs, cache eligibility, and
phase outcome. Later tasks add typed budget, approval, detector, containment,
and ledger assertions to the same object without changing format 2.

Current-candidate comparison separately reports newly allowed, warned,
approval-required, blocked, withheld, rule-trace, cache, completeness, budget,
approval, detector, and ledger deltas. Newly allowed or newly blocked cases
require an exact reviewed manifest binding case ID, old result, new result,
candidate lock digest, rationale, and expiry or permanent status. Wildcards,
stale entries, orphan entries, changed identities, and missing rationale fail.

Completeness enumerates represented tools, phases, decisions, provenance
classes, outcome classes, and later state dimensions. Case count alone never
means complete.

Export stores no raw credential, header, token, secret-shaped field, complete
tool result, environment value, or physical path. Selected values use the same
keyed identity and completeness rules as the ledger. Text, JSON, JUnit, SARIF,
and GitHub summary render from one bounded typed report with stable case IDs.

## Control Evidence

Contract table AP-T23. Owner: `internal/actionevidence`. Vectors:
`CONTROL-*`. Evolution: a framework source, status rule, or evidence selector
change requires a new mapping-pack version and primary-source review.

| Status | Exact meaning |
|---|---|
| `covered` | Every exact technical evidence selector for the control is present, current, trusted, and integrity-verified |
| `partial` | At least one required selector is covered and at least one is missing, incomplete, stale, or lower-provenance |
| `missing` | Required technical evidence is absent or fails integrity |
| `not_evaluated` | The mapping or evidence window was not evaluated; no coverage inference is made |

The versioned control-map record contains stable control ID, framework and
edition/date, mapping-pack identity, primary-source reference, bounded
rationale, exact evidence selectors, known gaps, review status, and required
completeness. Built-in mappings for SOC 2, GDPR, HIPAA, or the EU AI Act are
added only after TASK 163 re-verifies the current primary source and permitted
quotation boundary.

Export verifies policy and lock identity, retained ledger chain and range,
approval receipts, scenario results, budget state, authority provenance,
evidence window, and archive completeness before assigning status. Failure
downgrades `covered` to `partial` or `missing`. `not_evaluated` never becomes
another status by inference. A strict digest-pinned or signed custom mapping
pack may add a mapping but cannot override evidence facts, integrity,
provenance, completeness, or promote a weaker status.

Output is deterministic local JSON or Markdown containing safe identifiers,
digests, counts, categories, coverage, and gaps. It never contains raw
arguments, results, secrets, or personal data and never says certified,
compliant, guaranteed, regulator-approved, or legally sufficient.

## Proposed CLI Surface

Contract table AP-T24. Owner: `internal/commandmeta`. Vectors: `CLI-*`.
Evolution: command registration precedes dispatch, help, docs, completion, and
manpage generation in the same implementation task.

| Proposed command | Contract |
|---|---|
| `reconc mcp gateway [repo] --server LABEL (--expect-lock-digest SHA256 \| --allow-repository-managed-policy) [trusted-context flags] -- COMMAND [ARG...]` | Start one local tool-only gateway |
| `reconc why action [repo]` | Explain canonical action policy and lowering |
| `reconc action log tail\|stats\|verify\|export [repo]` | Read and verify the action ledger |
| `reconc action evidence export\|verify [repo]` | Produce local control-evidence mappings |

Trusted-context flags are operator inputs before `--`:
`--principal`, `--role`, `--environment`, repeated
`--credential-label`, `--run-id`, `--session-id`,
`--approval-authorities`, `--server-cwd`, repeated `--inherit-env`, and
`--timeout`. Values after `--` are only the downstream command and argv.
Environment values are inherited by exact operator-selected name, bound through
keyed value identities, and never rendered or persisted.

The downstream environment is empty by default. `--inherit-env NAME` adds one
exact inherited name after validating platform syntax; duplicates and
case-collisions on Windows are rejected. `--server-cwd` defaults to the
canonical repository root. Reconc resolves the downstream executable to an
absolute regular executable before replacing the child environment. Operator
configuration and approval-authority files must be outside the canonical
repository and outside any path writable by the agent for independent-authority
claims; otherwise startup refuses that claim.

These commands are proposed and unavailable while this RFC is Draft. TASK 154
owns `why action`, TASK 160 owns ledger commands, TASK 161 owns
`mcp gateway`, and TASK 163 owns evidence commands.

## Schema And Versioning

The canonical format-5 policy lock stores one `actions` plan and no parallel
`mcp` runtime plan. The schema URL and introduction tag are intentionally not
minted in this Draft. TASK 165 must first restore per-artifact schema truth, and
Christopher must explicitly select the exact release version that owns any new
immutable URL.

The contract rules are:

- legacy lock formats remain immutable migration inputs;
- v4-to-v5 migration lowers the exact existing MCP contract without inventing
  argument inspection or changing host behavior;
- an additive field with a safe explicit default may bump its owning artifact
  format version only when every consumer rejects unsupported future versions;
- removal, repurposing, type change, semantic default change, decision-order
  change, signed-field change, or identity change requires a new schema URL and
  contract version;
- current URLs resolve only in publication verification. Offline runtime never
  needs network access.

This RFC does not redefine policy-source precedence. TASK 154 must make the
source loader match the existing `policy.SourcePrecedence` authority and prove
the AGENTS/CLAUDE order explicitly before action compilation is accepted.

## Deterministic Conformance Vectors

Contract table AP-T25. Owner: each vector prefix's package owner. Vectors: all
rows in this table. Evolution: vectors are append-only within contract version
1; changing an expected result is a breaking contract change.

| ID | Input summary | Exact expected result |
|---|---|---|
| `EXT-001` | MCP 2026 client lists and calls one tool | current tool result shape accepted |
| `EXT-002` | MCP 2025 legacy client calls supported tool | legacy mapping preserves category |
| `EXT-003` | client requests prompts or resources | capability absent or explicit unsupported |
| `OWN-001` | pure evaluator test imports MCP SDK | dependency gate fails |
| `OWN-002` | gateway bypasses production evaluator | mutation test fails |
| `TRUST-001` | operator-bound environment matches configured block rule | block |
| `TRUST-002` | host-observed resolved identity matches sole allow rule | allow |
| `TRUST-003` | adapter-asserted role attempts allow | indeterminate, then block |
| `TRUST-004` | agent-supplied role attempts allow | indeterminate, then block |
| `AUTH-001` | expected digest matches fresh lock | startup eligible |
| `AUTH-002` | expected digest drifts | startup or dispatch blocks |
| `AUTH-003` | explicit repository-managed fresh lock | eligible with lower provenance |
| `AUTH-004` | both or neither authority flags | usage failure |
| `CFG-001` | unknown field at every action depth | compile failure |
| `CFG-002` | later-owned inert detector field before TASK 159 | compile failure |
| `TOOL-001` | exact declared stdio tool | one declaration selected |
| `TOOL-002` | gateway name is 128 ASCII characters | accepted |
| `TOOL-003` | gateway name is 129 ASCII characters | rejected |
| `TOOL-004` | host name is 256 UTF-8 bytes | accepted |
| `TOOL-005` | host name is 257 UTF-8 bytes | rejected |
| `TOOL-006` | legacy and action declarations share identity | compile failure |
| `DEFAULT-001` | declared tool, no rule | allow baseline |
| `DEFAULT-002` | undeclared gateway tool | block |
| `DEFAULT-003` | legacy unclassified host | allow at Reconc boundary |
| `DEFAULT-004` | legacy unclassified deny | block |
| `RULE-001` | block and allow both match | block |
| `RULE-002` | approval and warn both match | require approval |
| `RULE-003` | present empty selector list | compile failure |
| `REQ-001` | argument object has reordered keys | identical canonical request |
| `REQ-002` | duplicate key at nested depth | invalid request |
| `REQ-003` | invalid UTF-8 or unpaired surrogate | invalid request |
| `REQ-004` | `1.0` and `10e-1` | canonical number `1` |
| `PHASE-001` | pre-call block rule reads arguments and context | block before dispatch |
| `PHASE-002` | post-result allow after a pre-call block | original block remains, no dispatch |
| `PHASE-003` | progress block after dispatch | suppress event, side-effect status unchanged |
| `PHASE-004` | observation block candidate | record decision, no dispatch or delivery mutation |
| `AST-001` | all(false, indeterminate) | false |
| `AST-002` | all(true, indeterminate) | indeterminate |
| `AST-003` | any(true, indeterminate) | true |
| `AST-004` | any(false, indeterminate) | indeterminate |
| `AST-005` | not(indeterminate) | indeterminate |
| `PRED-EXISTS-01` | present null | true |
| `PRED-EXISTS-02` | missing pointer | false |
| `PRED-EQ-01` | decimal `1.0` equals `1` | true |
| `PRED-EQ-02` | string `1` equals number `1` | false |
| `PRED-NEQ-01` | distinct same-type scalars | true |
| `PRED-NEQ-02` | missing target | indeterminate |
| `PRED-IN-01` | scalar in same-type list | true |
| `PRED-IN-02` | mixed operand types | compile failure |
| `PRED-NOTIN-01` | scalar absent from list | true |
| `PRED-NOTIN-02` | null target | indeterminate |
| `PRED-PREFIX-01` | exact UTF-8 prefix | true |
| `PRED-PREFIX-02` | case differs | false |
| `PRED-SUFFIX-01` | exact UTF-8 suffix | true |
| `PRED-SUFFIX-02` | normalized-equivalent Unicode only | false |
| `PRED-CONTAINS-01` | exact sequence present | true |
| `PRED-CONTAINS-02` | wrong target type | indeterminate |
| `PRED-GLOB-01` | `a/b` against `a/**` | true |
| `PRED-GLOB-02` | malformed pattern | compile failure |
| `PRED-REGEX-01` | whole string satisfies RE2 | true |
| `PRED-REGEX-02` | substring only satisfies pattern | false |
| `PRED-GT-01` | `1e100` greater than `9e99` | true |
| `PRED-GT-02` | string numeric target | indeterminate |
| `PRED-GTE-01` | equal exact decimals | true |
| `PRED-GTE-02` | smaller target | false |
| `PRED-LT-01` | negative number below zero | true |
| `PRED-LT-02` | equal number | false |
| `PRED-LTE-01` | equal exact decimals | true |
| `PRED-LTE-02` | greater target | false |
| `PRED-URL-01` | exact HTTPS host and normalized port | true |
| `PRED-URL-02` | userinfo or encoded slash ambiguity | indeterminate |
| `PRED-CIDR-01` | IPv4 address in canonical prefix | true |
| `PRED-CIDR-02` | address-family mismatch | false |
| `PRED-PATH-01` | lexical child on segment boundary | true, restrictive evidence only |
| `PRED-PATH-02` | lexical sibling prefix | false |
| `PRED-PATH-03` | unresolved parent escape | indeterminate |
| `DEC-001` | only allow | allow |
| `DEC-002` | allow plus warn | warn |
| `DEC-003` | warn plus approval | require approval |
| `DEC-004` | approval plus block | block |
| `DEC-005` | post-result block after success | withhold, dispatch status succeeded |
| `ERR-001` | stale policy before dispatch | `policy_stale`, block |
| `ERR-002` | evaluation deadline | `deadline_exceeded`, block |
| `ERR-003` | unsupported result content | `unsupported_content`, withhold |
| `ERR-004` | downstream EOF after dispatch | `downstream_outcome_unknown`, withhold all partial content |
| `ERR-005` | invalid request shape | `invalid_request`, block |
| `ERR-006` | duplicate JSON key | `duplicate_key`, block |
| `ERR-007` | invalid UTF-8 | `invalid_utf8`, block |
| `ERR-008` | schema-invalid arguments | `schema_invalid`, block |
| `ERR-009` | resource limit exceeded | `limit_exceeded`, block |
| `ERR-010` | policy absent | `policy_missing`, block |
| `ERR-011` | expected lock digest differs | `lock_mismatch`, block |
| `ERR-012` | source used in wrong phase | `unsupported_phase`, block |
| `ERR-013` | undeclared gateway tool | `tool_unclassified`, block |
| `ERR-014` | discovered contract changed | `tool_contract_stale`, block |
| `ERR-015` | relaxing context below required provenance | `context_untrusted`, block |
| `ERR-016` | required keyed identity unavailable | `identity_unavailable`, block |
| `ERR-017` | rule condition remains indeterminate under default | `condition_indeterminate`, block |
| `ERR-018` | cumulative capacity exhausted | `budget_exhausted`, block |
| `ERR-019` | state store unavailable | `state_unavailable`, block |
| `ERR-020` | state store malformed | `state_corrupt`, block |
| `ERR-021` | abandoned post-dispatch reservation | `reservation_indeterminate`, retry blocked |
| `ERR-022` | valid rule needs receipt | `approval_required`, no dispatch |
| `ERR-023` | authority signs rejection | `approval_rejected`, block |
| `ERR-024` | signature or binding invalid | `approval_invalid`, block |
| `ERR-025` | receipt expired | `approval_expired`, block |
| `ERR-026` | nonce or call already consumed | `approval_replayed`, block |
| `ERR-027` | authority unavailable | `authority_unavailable`, block |
| `ERR-028` | pre-call detector cannot complete | `inspection_incomplete`, block |
| `ERR-028B` | post-result detector cannot complete | `inspection_incomplete`, withhold |
| `ERR-029` | result is intentionally contained | `result_withheld`, withhold |
| `ERR-030` | required ledger cannot append | `ledger_unavailable`, block before dispatch |
| `ERR-031` | ledger chain malformed under required recording | `ledger_corrupt`, block |
| `ERR-032` | downstream cannot launch | `downstream_unavailable`, no dispatch |
| `ERR-033` | authoritative downstream error | `downstream_error`, safe error result |
| `ERR-034` | invalid MCP tool-call frame | `protocol_error`, fail call with no dispatch |
| `ERR-035` | upstream cancellation before dispatch | `cancelled`, block and release reservation |
| `ERR-036` | gateway shutdown | `shutdown`, block new call and cancel owned call |
| `ERR-037` | unreachable pre-call internal state | `internal_invariant`, block |
| `ERR-037B` | unreachable post-result internal state | `internal_invariant`, withhold |
| `ID-001` | missing HMAC key for persisted argument identity | identity omitted, incomplete |
| `ID-002` | same value under two domains | different keyed identities |
| `CACHE-001` | every identity exact and resampled | eligible |
| `CACHE-002` | one context provenance changes | miss |
| `CACHE-003` | pending approval | non-cacheable |
| `BUD-001` | two processes reserve final slot | exactly one succeeds |
| `BUD-002` | crash after dispatch | reservation remains indeterminate |
| `BUD-003` | policy or key rotates | no fresh capacity |
| `APPROVAL-001` | exact valid signature and unused nonce | consume once |
| `APPROVAL-002` | same receipt consumed twice | second use blocks |
| `APPROVAL-003` | altered argument, policy, principal, or reservation | signature/binding rejection |
| `SCAN-001` | selected synthetic secret with configured block detector | block before dispatch |
| `SCAN-002` | unsafe text result after successful side effect | withhold, success remains explicit |
| `SCAN-003` | unsupported binary with no allow policy | withhold |
| `LEDGER-001` | raw synthetic secret crosses every lifecycle | serialized ledger contains none |
| `LEDGER-002` | archive missing | chain range incomplete, never complete |
| `LIMIT-001` | every AP-T19 byte/count limit minus one, exact, plus one | accept, accept, reject |
| `TIME-001` | every AP-T20 boundary before, at, after deadline | complete, `deadline_exceeded`, `deadline_exceeded` |
| `FAIL-001` | blocked request | fake downstream invocation count remains zero |
| `FAIL-002` | withheld result contains seeded marker | upstream bytes contain no marker |
| `IMPACT-001` | action case omits expected decision | corpus invalid |
| `IMPACT-002` | newly allowed case lacks exact delta manifest | CI failure |
| `CONTROL-001` | complete current trusted synthetic evidence | `covered` |
| `CONTROL-002` | one required evidence class is incomplete | `partial` |
| `CONTROL-003` | required chain integrity fails | `missing` |
| `CONTROL-004` | mapping was not run | `not_evaluated` |
| `CONTROL-005` | custom mapping prose claims absent evidence | status remains `missing` |
| `CLI-001` | command appears in docs while RFC is Draft | publication gate requires explicit unavailable label |
| `COMPAT-001` | each valid legacy effect lowers to format 5 | identical current host decision and evidence effect |
| `COMPAT-002` | legacy unclassified host or deny | exact host-unmatched allow or block |
| `COMPAT-003` | fingerprinted call with only unqualified declaration | no fallback |
| `COMPAT-004` | valid format-4 lock migrates twice | byte-identical format-5 result and decision |
| `COMPAT-005` | legacy and action declarations are disjoint | compile succeeds with one canonical plan |

Every implementation task must convert the vectors it owns into table-driven
tests that fail under the corresponding negative mutation. The prose table is
not a substitute for executable proof.

## Freeze Conditions

This RFC may move from Draft to Frozen only when:

- TASK 154 through TASK 164 and TASK 165 satisfy their acceptance;
- the format-5 schema and every new artifact have truthful immutable registry
  identities;
- all AP-T25 vectors are executable against production owners;
- the real Go gateway passes current and supported legacy MCP conformance;
- the disposable LangChain consumer proof uses no Reconc-authored adapter;
- release output, docs, help, schemas, SBOM, and provenance agree;
- a source-first contradiction pass finds no ambiguous default, unowned state,
  circular dependency, unsupported claim, or bypass represented as enforced.

Until then this document remains a proposed contract only.
