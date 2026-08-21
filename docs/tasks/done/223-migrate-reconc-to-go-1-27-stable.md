# TASK 223: Migrate Reconc to Go 1.27 stable

## Why

Reconc and its portable harness currently declare Go 1.26 while the development
system already runs the stable Go 1.27.0 release. The repository toolchain,
continuous-integration lint contract, contributor documentation, generated
harness pack, and verification evidence must agree on one stable toolchain.
Go 1.27 also changes JSON execution and compression internals that materially
affect Reconc hot paths and deterministic generated archives, so the migration
requires measured performance and artifact-integrity checks rather than a
declaration-only version bump.

## Acceptance

- The root module and portable harness declare Go 1.27, and all repository-owned
  development and continuous-integration toolchain contracts resolve from that
  declaration.
- Staticcheck uses the latest stable release compatible with Go 1.27 in the
  Makefile, CI, release workflow, trust assertions, and documentation.
- Compatibility fixtures that model external repositories remain intentionally
  version-independent; no historical evidence or unrelated fixture is rewritten
  merely to remove the text `1.26`.
- The committed harness manifest and archive are regenerated with Go 1.27 and
  pass byte-integrity, publication, and release-trust checks.
- Focused Go 1.26-versus-1.27 benchmarks quantify relevant Reconc hot-path
  effects without claiming unsupported global speedups.
- Formatting, module tidiness, vet, Staticcheck, root and portable-template race
  suites, publication audit, release trust, and self-hosting pass with Go 1.27.
- Go 1.27 feature opportunities, including experimental SIMD, are mapped to
  concrete Reconc code paths with explicit benefit, stability, and verification
  requirements.

## Sub-Tasks

- [x] Audit toolchain, CI, documentation, generated-artifact, and hot-path surfaces
- [x] Update stable Go and Staticcheck contracts
- [x] Regenerate and verify Go-version-sensitive harness artifacts
- [x] Run focused and complete Go 1.27 verification gates
- [x] Record measured feature findings and archive TASK 223

## Notes

- The system Go installation is Homebrew Go 1.27.0 at
  `/opt/homebrew/bin/go`; no system installation change is required.
- On identical pre-migration source, Go 1.27 reduced the median
  `BenchmarkLoadLockfile` result from about 122 microseconds and 908 allocations
  to about 102 microseconds and 416 allocations. The isolated lock-decode stage
  fell from about 54.5 to 28.5 microseconds and from 907 to 338 allocations.
- The maximum-legal structured-JSON inspection benchmark improved from about
  50.0 to 38.8 milliseconds at the median. The representative small payload was
  effectively flat, so the evidence does not support a universal JSON speedup.
- Stable Staticcheck v0.8.0 passes under Go 1.27. Staticcheck v0.7.0 cannot import
  Go 1.27 export data and therefore must be upgraded with the toolchain.
- Go 1.27 SIMD APIs require `GOEXPERIMENT=simd` and are not API-stable. They are
  audit candidates only, not part of the stable migration without separate
  benchmark and compatibility evidence.
- The first full race run exposed one expected Go 1.27 JSON error-text change:
  truncated proof-bundle JSON now reports `unexpected end of JSON input`
  instead of an `EOF` string. The test now asserts stable malformed-input
  classification and rejection rather than standard-library wording.
- Two MCP end-to-end timing assertions first failed only during the saturated
  full race run and then passed three consecutive focused race executions.
  A repeated saturated run reproduced delayed progress-handler delivery after
  the tool result, proving the test's immediate channel-length assertion was
  racy. The test now awaits the exact notification sequence and rejects extras
  within a bounded post-delivery window; production behavior is unchanged.
- A concurrent external `go clean -cache` removed shared build-cache entries
  during the second full race run. Complete reruns therefore use a private
  temporary `GOCACHE` so another local session cannot invalidate verification.
- Go 1.27 automatically backs the existing `encoding/json` API with its v2
  implementation while preserving v1 semantics. This is the source of the
  measured lockfile-decoding gains and requires no contract migration.
- The new stable `encoding/json/jsontext` token API is a credible separate
  follow-up for `internal/action/value.go` and strict lockfile/proof decoders:
  it rejects duplicate names and invalid UTF-8 by default and exposes depth and
  token state. Adoption must retain Reconc's decimal normalization, aggregate
  item limits, error classes, canonical escaping, and differential fuzz corpus.
- A Go 1.27 CPU profile of maximum-legal action inspection attributed about
  26 percent of samples to standard-library string search, 19 percent to
  lowercasing, 17 percent to JSON quoting, 14 percent to Unicode normalization,
  and 9 percent to optimized SHA-256. Allocation space was dominated by
  canonical `Value.MarshalJSON` buffer growth and cloning. The best follow-up
  is cached or single-pass canonical encoding, not custom SIMD.
- Experimental SIMD is not suitable for the stable Reconc build. The relevant
  scans are branch-heavy and bounded, while `strings`/`bytes` search and SHA-256
  already use architecture-tuned implementations. A SIMD ASCII fast path for
  `ValidateJSONUnicode` is worth considering only behind a build experiment
  after representative arm64/amd64 benchmarks and differential fuzzing.
- `runtime/pprof`'s generally available `goroutineleak` profile can strengthen
  diagnostics around MCP child lifecycle and persistent hook workers. It should
  be introduced as a targeted post-shutdown regression diagnostic, not a global
  zero-count assertion that includes unrelated test-runtime goroutines.
- `testing/synctest.Sleep` can replace selected scheduler sleeps in pure
  in-memory coordination tests. File-lock, subprocess, Git, and filesystem
  timing tests cannot safely move into a synthetic-time bubble.
- `bytes.CutLast` and `strings.CutLast` can simplify several last-separator
  parsers, but the paths are not measured hot spots. Generic methods, embedded
  field literal keys, UUID, and ML-DSA have no current Reconc benefit that
  justifies contract or readability churn.
- Go 1.27 requires macOS 13 Ventura or later. The repository and contributor
  documentation now state that native build floor explicitly; existing Linux
  and Windows release targets are unchanged.
- Final Go 1.27 verification passed module tidy checks, formatting, vet, stable
  Staticcheck v0.8.0, focused compatibility tests, five repeated MCP progress
  race tests, both complete module race suites, publication audit, harness-pack
  integrity, release-trust, self-hosting, the hash-pinned LangChain proof on
  Python 3.13.14, and both Govulncheck v1.6.0 scans with no vulnerabilities.
- Coverage measurement passed at 82.7311 percent for the root module and
  84.0288 percent for the portable harness module.
- Release-trust built and mutated one host-target release fixture only inside
  its disposable temporary directory. No tag, workflow dispatch, remote
  publication, push, or user-facing release was created.

## Deviations

None.
