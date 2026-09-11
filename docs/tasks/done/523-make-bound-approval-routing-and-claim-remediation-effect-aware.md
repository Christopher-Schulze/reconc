# TASK 523: Make bound approval routing and claim remediation effect-aware

## Why

Bound authority approval correctly fails closed when a mutating command does
not declare exact repository write paths. The current route is nevertheless too
broad after exact effects are available: if any blocking bound-approval rule
exists anywhere in the policy, a command whose declared writes are completely
disjoint from every protected `when_paths` still fails because its matched
bound-rule list is empty. This rejects a proven non-authority change instead of
running the ordinary pre-write policy check.

The same workflow exposes inconsistent claim remediation. A structured FixPlan
emits `reconc check --claim <name>`, which supplies evidence only to one
evaluation, while agent-session guidance requires `reconc hook claim . <name>`
to persist the claim in session state. This task consolidates audit candidates
C218 and C219 around one effect-bound authority and remediation contract.

## Acceptance

- Preserve the parser and runtime-plan requirement that every `require_claim`
  rule has non-empty `when_paths` and `claims`; malformed rules must never enter
  a current compiled policy.
- Keep known read-only commands outside the native approval path and keep
  incomplete or potentially mutating commands fail-closed when exact write
  effects are absent and any bound authority rule could apply.
- Normalize declared `reconc_write_paths` once through the canonical prospective
  repository-path contract before policy matching or receipt binding.
- If declared writes match no bound authority rule, reject any stale or
  inapplicable approval envelope, run the ordinary pre-write policy decision,
  and allow the command when that decision passes.
- If one or more declared writes match bound authority rules, require the exact
  sorted rule IDs, normalized paths, complete command effect, policy generation,
  session state version, principal, expiry, and `tool_use_id` already required
  by the native approval receipt.
- Never permit a signed receipt for one subset of declared writes to authorize
  additional protected paths, a changed command, a changed alias expansion, or
  a different session/policy generation.
- Produce a precise capability diagnostic for hosts that cannot provide exact
  command effects; do not misreport a disjoint declared command as an authority
  change or suggest an impossible approval.
- Represent claim remediation according to execution context. Standalone
  evaluation may retain `reconc check --claim <name>`; an active agent session
  must receive a persistent `reconc hook claim <repo> <name>` action bound to
  the intended session, or an explicitly non-executable action if that context
  is unavailable.
- Preserve argv boundaries, repository identity, authorization metadata,
  required-claim metadata, deterministic ordering, output bounds, and existing
  FixPlan compatibility rules. A shape change must use the established schema
  compatibility process without changing the product version.
- Add end-to-end regressions for read-only commands, undeclared mutating
  commands, declared disjoint writes, mixed protected/unprotected writes,
  exact protected writes, stale envelopes, replay, concurrent state changes,
  standalone claim checks, and persistent session claims.
- Update authority-approval, hook capability, FixPlan, and agent workflow
  documentation so each route has one executable and truthful remediation.
- Pass focused native-approval, action-state, runtime, CLI, adapter, schema, and
  race tests plus all required repository gates.

## Sub-Tasks

- [x] Read all native-approval signatures, payload adapters, receipt verification and consumption, bound-rule matching, command classification, FixPlan builders, saved-report consumers, and claim-state mutations.
- [x] Specify the command-effect decision table for read-only, unknown, declared-disjoint, declared-protected, mixed, stale-envelope, and malformed-envelope inputs.
- [x] Refactor the existing approval route so exact disjoint effects use ordinary policy evaluation while unknown and protected effects retain fail-closed approval semantics.
- [x] Add one context-aware claim-remediation construction path and propagate the required session/repository identity through existing callers without parallel FixPlan families.
- [x] Cover effect normalization, rule matching, receipt binding/consumption, replay, context drift, and standalone/session remediation with deterministic tests.
- [x] Verify every shipped adapter either carries the required fields or emits the documented unsupported-capability result; do not infer unavailable host metadata.
- [x] Update schemas and documentation only where the final externally visible contract changes, preserving existing compatible readers.
- [x] Run focused race tests and all required repository gates; re-read changed files and archive only after the full authority path is proven.

## Notes

### Observed behavior

- Parser and runtime-plan already require both `when_paths` and `claims`.
  The audit theory based on a valid pathless bound rule is false.
- The live overrestriction was `handlers.go` rejecting `len(boundRuleIDs) == 0`
  after exact declared writes were available.
- Incomplete command analysis remains fail-closed via `commandMayWriteRepository`.
- Shipped adapters do not invent `reconc_write_paths`; undeclared mutating
  commands now get a capability diagnostic instead of a fake authority receipt
  prompt.

### Implementation

- Declared disjoint writes reject leftover envelopes, run ordinary pre-write
  policy, and skip native receipt consumption.
- Read-only commands may reuse the pre-decision cache even when bound authority
  rules exist; mutating commands still bypass that cache.
- One FixPlan family: `AttachClaimRemediation` selects `check --claim`,
  `hook claim --session`, or a non-executable `assert_claim` inspection action.
  Format version 2 is unchanged.

### Security invariants

- No effect declaration means no authority bypass.
- An empty matched-rule set is permission only when exact declared effects are
  valid, complete, disjoint, and ordinary policy evaluation passes.
- Receipt verification and consumption stay atomic with the session generation.
- A remediation action communicates required work; it never grants approval.
- No command-name whitelist may replace effect binding.

### Verification matrix

- Zero, one, and multiple bound rules; overlapping and disjoint patterns.
- One and multiple declared writes; duplicates; lexical aliases; symlinks;
  prospective missing paths; outside-root and malformed paths.
- Missing, valid, stale, mismatched, replayed, and extraneous receipts.
- Known read-only, statically mutating, dynamically unresolved, redirected, and
  alias-expanded commands.
- No session, implicit active session, explicit session, wrong session, rotated
  state, saved report, direct check, and adapter-supplied remediation contexts.

## Deviations

None.
