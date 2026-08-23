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

- [x] Add event-count and lockfile-stage benchmarks with allocation profiles
- [x] Replace per-event `MergedWith` with one bounded accumulator
- [x] Design the strict current-format raw-envelope decoder
- [x] Capture rule field presence during the strict token pass
- [x] Decode current rules/actions/envelope without interface boxing or re-marshaling
- [x] Preserve the map-based migration path for every legacy format
- [x] Add duplicate-key, missing-field, number, depth, maximum-size, and migration tests
- [x] Update runtime lockfile and execution-input documentation plus benchmark history
- [x] Run fuzz, schema, migration, runtime, CLI, and complete verification

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
- Event ingestion now performs a non-failing kind count for capacity planning,
  clones bulk collections once, and appends validated events directly. Error
  order, bulk-first ordering, write epochs, duplicate behavior, and path bytes
  remain unchanged; the former per-event six-container allocation and complete
  accumulated-copy path are gone.
- Text inputs pass a 64-level and 262,144-item strict token admission before
  generic decoding. Direct structured callers receive the same aggregate
  evidence cardinality bound.
- Current format-6 locks are selected only after strict bounded JSON admission.
  Top-level fields are retained as raw messages, the envelope/rules/actions are
  decoded directly, and canonical raw `rules`/`actions` remain outside the
  compatibility interface tree. Rule, check, and assurance field layouts are
  collected by `jsontext` token scans instead of per-object raw maps.
- Formats 1 through 5 still enter the original generic migration table. A
  dedicated benchmark exercises every supported format, and existing parity,
  digest, schema, and malformed-field tests remain unchanged.
- Performance history advanced to `reconc.performance-history/v8`. Apple M1
  medians: 256 events 38.15 us/28,841 B/520 allocs; 8,192 events 1.81 ms/
  1,016,795 B/24,358 allocs; 64-rule current lock 320.99 us/222,524 B/568
  allocs; maximum 4,096-rule lock 16.15 ms/19,571,611 B/12,957 allocs.
- Verification: full runtime race suite; all six migration-format benchmarks;
  three fuzz runs totaling 378,923 executions; benchmark record/baseline/
  compare; `make test-fast`; `make vet`; `make lint`; and
  `make publication-audit` passed. The one cumulative full race/release gate is
  intentionally retained for TASK 283 so the same six-minute suite is not run
  once per independent task.

## Deviations

None.
