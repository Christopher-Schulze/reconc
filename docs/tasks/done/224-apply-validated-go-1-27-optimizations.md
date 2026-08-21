# TASK 224: Apply validated Go 1.27 optimizations

## Why

The Go 1.27 migration identified a concentrated allocation hot path in canonical
action JSON encoding and new stable diagnostics and test-time concurrency tools
that can strengthen Reconc without changing product contracts. The follow-up
must extract only measured or structurally proven value: canonical bytes,
decimal normalization, limits, error classes, identities, shutdown behavior,
and portable harness parity must remain stable. Experimental or unrelated Go
1.27 features must not create churn without a concrete Reconc benefit.

## Acceptance

- Canonical action JSON encoding uses a lower-allocation implementation whose
  output is byte-identical to the existing contract across focused edge cases,
  the deterministic corpus, and fuzz regressions.
- Direct `encoding/json/jsontext` use is adopted only where duplicate-name,
  invalid-UTF-8, surrogate, depth, cardinality, decimal, trailing-input, and
  error-class behavior remains explicitly covered and compatible.
- Maximum-legal structured-action benchmarks report before-and-after latency,
  bytes, and allocations without unsupported global performance claims.
- Targeted MCP or hook-worker shutdown tests use the Go 1.27 goroutine-leak
  profile as a bounded regression diagnostic without asserting that the whole
  test process has zero unrelated goroutines.
- `testing/synctest` replaces wall-clock coordination only in tests whose
  dependencies are fully in-memory; filesystem, lock, Git, and subprocess
  timing remains on real time.
- `CutLast`, generic methods, UUID, ML-DSA, and experimental SIMD receive a
  concrete source-and-contract decision; production code changes only where
  benefit exceeds compatibility and maintenance cost.
- Root and portable-harness formatting, tidiness, vet, Staticcheck, race,
  publication, release-trust, self-hosting, vulnerability scans, and coverage
  measurements complete successfully with Go 1.27.

## Sub-Tasks

- [x] Establish canonical JSON, concurrency, and candidate baselines
- [x] Optimize canonical JSON with byte-contract and fuzz parity
- [x] Add targeted shutdown leak diagnostics and valid synthetic-time tests
- [x] Resolve the remaining Go 1.27 candidates without unnecessary churn
- [x] Flush documentation, run full verification, and archive TASK 224

## Notes

- TASK 223 measured canonical `Value.MarshalJSON` buffer growth and cloning as
  the dominant allocation-space source for the maximum-legal action payload.
- The stable `encoding/json/jsontext` API is a candidate, not a requirement:
  HTML/JavaScript escaping and error behavior must be compared before it can
  replace any identity-bearing encoder or strict decoder path.
- A repository graph is not present. The checkout contains more than the
  Graphify full-corpus threshold, so implementation evidence comes from direct
  source, caller, test, benchmark, and gate inspection rather than an unbuilt
  graph.
- The action decoder and strict lockfile decoder now use the stable
  `encoding/json/jsontext` token API. Differential references retain the prior
  `encoding/json.Decoder` behavior as a test oracle. Focused fuzzing completed
  140,681 action-decoder executions, 84,081 exact encoder-escaping executions,
  and 46,417 lockfile-decoder executions without a parity failure.
- Canonical values now append into one capacity-hinted output slice. Ordinary
  strings use `jsontext.AppendQuote`; values containing `<`, `>`, `&`, U+2028,
  or U+2029 use the legacy encoder to preserve exact identity-bearing bytes.
  The maximum-legal 8 MiB encoder moved from a five-run median of 16.006 ms,
  23,191,586 B/op, and 18 allocs/op to 10.274 ms, 8,388,611 B/op, and 1
  alloc/op. The complete structured-action path moved from 72.828 ms,
  22,852,399 B/op, and 118 allocs/op to 41.067 ms, 8,396,832 B/op, and 100
  allocs/op. Allocation reductions are the durable result; the Apple M1
  latency measurements are local observations rather than global guarantees.
- The MCP gateway shutdown test queries the Go 1.27 `goroutineleak` profile for
  the exact refresh-worker stack after `Gateway.Close`. It intentionally does
  not assert global process-wide emptiness. The audit append gate tests use
  `testing/synctest` because their path is only an in-memory map key and their
  synchronization is channel-, context-, and timer-based. Git, process,
  filesystem, file-lock, and mutex-waiter tests remain on real time.
- Candidate decisions: `CutLast` would only reshuffle already-correct short
  splits, some of which require byte offsets; generic methods do not improve
  the package-level generic helpers because none is receiver-owned; Reconc
  preserves externally supplied UUID-like session IDs but does not generate
  them; ML-DSA would break the Ed25519 approval signature and receipt contract;
  experimental SIMD has no stable API and the measured paths already use
  optimized standard-library primitives. No production churn was introduced
  for these candidates.
- The first focused race run caught changed `jsontext` wording for duplicate
  lockfile keys and malformed Unicode. Production now maps those failures back
  to the existing Lockfile error contract; the unchanged strict-input tests and
  the repeated runtime race suite pass.
- Final verification passed module-tidy diffs, formatting, vet, Staticcheck
  v0.8.0, the complete root and portable-harness race suites, publication and
  harness-pack audits, release trust, self-hosting, coverage measurement, both
  Govulncheck v1.6.0 scans, and the hash-pinned LangChain MCP proof with its
  disposable Python 3.13.14 environment. Release trust exposed and this TASK
  corrected one pre-existing TASK 223 sentence that accidentally expressed a
  numeric coverage result as a pass contract.
- No release asset, user-facing release, tag, workflow dispatch, remote push,
  or publication was created. Release-trust used only its disposable fixture.

## Deviations

None.
