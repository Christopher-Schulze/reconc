# TASK 276: Linearize execution input and lockfile decoding

## Why

`LoadExecutionInputs` merges each event by copying every accumulated slice/map
again, making event ingestion O(E²). Current-format lockfiles are first decoded
into `map[string]interface{}`, large rules/actions are marshaled back to JSON,
the envelope is marshaled again for typed decoding, and rules are unmarshaled a
second time for field-presence validation. This repeated full-payload work is
paid by every one-shot CLI process.

## Acceptance

- Execution inputs append directly into one capacity-planned accumulator.
  Event order, write epochs, duplicate handling, exact indexed errors, bulk plus
  event merge order, and normalized output remain unchanged.
- Parsing enforces aggregate item/byte limits before large allocations and does
  not allocate six empty containers for every single event.
- Current-format lockfiles use one strict token/raw-message pass followed by one
  typed decode. Duplicate keys, depth/item budgets, unknown fields, required
  field presence, number exactness, counts, self-digest, and action-plan
  validation remain fail closed.
- Legacy lockfile formats still route through the migration table and produce
  the same in-memory current payload. The fast path is selected only after an
  authenticated/strict format discriminator is available.
- Rule kind-specific field-presence checks consume raw object metadata captured
  during the strict pass rather than unmarshaling every rule again.
- Large arrays are not boxed into `interface{}` on the current-format path, and
  rules/actions are not re-marshaled solely to recover raw bytes.
- Benchmarks cover event-count scaling, mixed bulk/events, current maximum
  lockfiles, cold one-shot load, warm evaluator reuse, and every supported
  migration format.
- Fuzz, migration, schema-identity, lock-diff, CLI, docs, and complete gates
  pass.

## Sub-Tasks

- [ ] Add event-count and lockfile-stage benchmarks with allocation profiles
- [ ] Replace per-event `MergedWith` with one bounded accumulator
- [ ] Design the strict current-format raw-envelope decoder
- [ ] Capture rule field presence during the strict token pass
- [ ] Decode current rules/actions/envelope without interface boxing or re-marshaling
- [ ] Preserve the map-based migration path for every legacy format
- [ ] Add duplicate-key, missing-field, number, depth, maximum-size, and migration tests
- [ ] Update runtime lockfile and execution-input documentation plus benchmark history
- [ ] Run fuzz, schema, migration, runtime, CLI, and complete verification

## Notes

- Current evidence: `internal/runtime/events.go:LoadExecutionInputs` assigns
  `merged = merged.MergedWith(parsed)` for every event; `MergedWith` copies all
  accumulated collections.
- Current evidence: `decodeLockfile` uses a strict interface tree, marshals
  `rules` and `actions`, `decodeRuntimeEnvelopeWithParts` marshals a second
  envelope, and `validateRuntimeRuleFieldPresence` unmarshals rules into raw
  maps again.
- A direct `encoding/json` struct decode alone is insufficient because Reconc's
  strict boundary also rejects duplicate keys and enforces bounded depth/items.
- Do not remove legacy migration or weaken current-vs-legacy schema identity to
  obtain the fast path.

## Deviations

None.
