# TASK 268: Correct hook runtime and lifecycle state reporting

## Why

Several hook lifecycle paths can report the wrong state or preserve a broken
worker. Custom-runtime freshness stores a per-manifest digest mismatch in a
loop-external variable, which degrades every following valid manifest. The
generated worker publishes its process before its ping is inside the guarded
handshake path, so a synchronous write failure can cache a dead worker. Codex
route accounting uses substring matching even though a boundary-aware token
matcher already exists. Partial installation failures can print a success-form
report before returning the error.

## Acceptance

- Each custom runtime receives an independent freshness result. A global
  evaluator/lockfile failure degrades all manifests, while one digest mismatch
  degrades only that runtime.
- A worker becomes reusable only after a clean ping response. Spawn, stderr
  drain, ping write, response parse, timeout, abort, and cleanup are one guarded
  state transition; no dead process remains cached after any failure.
- Codex route counts match complete runtime-event tokens, including prefix
  pairs such as `*-stop` and `*-stop-failure`, and ignore foreign commands that
  merely mention a route string.
- Hook install emits one unambiguous partial-failure object/text report. JSON
  consumers receive explicit success/failure state and do not need to infer it
  only from the process exit code.
- TOML comment handling respects quoted `#` characters and rejects duplicate or
  misplaced `features.hooks` values without maintaining divergent parsers for
  activation and status.
- Policy-author YAML extraction uses a structured rules renderer, not a prose
  substring marker. Live verification passes an explicit environment to child
  processes instead of mutating process-global environment state.
- Install publication revalidates the exact bytes/identity used to construct a
  merged artifact before replacing it, preserving concurrent user edits.
- Generated hook fixtures, installation/status/uninstall tests, worker protocol
  tests, concurrency/race tests, docs, and cross-platform gates pass.

## Sub-Tasks

- [x] Separate global and per-manifest custom-runtime freshness state
- [x] Make worker startup publication atomic with the complete handshake
- [x] Use exact route-token accounting for Codex budget validation
- [x] Define and render explicit partial-install failure contracts
- [x] Consolidate the bounded TOML boolean reader and quoted-comment handling
- [x] Expose structured policy-rule rendering for policy authoring
- [x] Replace process-global verification environment mutation with child-local configuration
- [x] Add compare-before-publish revalidation to merge-based hook installers
- [x] Add regression, adversarial replacement, generated-fixture, and platform tests
- [x] Update hook lifecycle documentation and run complete verification

## Notes

- Current evidence: `internal/cli/hook_custom_runtime.go` declares
  `freshnessError` before the source loop and overwrites it on a manifest digest
  mismatch without resetting it for the next source.
- Current evidence: generated code in `internal/hooks/worker_client.go` assigns
  `worker = current` and calls `writeWorkerFrame` before entering `try`.
- Current evidence: `internal/hooks/status.go:codexRouteBudgetIssues` uses
  `strings.Contains`; `containsRuntimeEventToken` in the same file implements
  the required boundary rule.
- Current evidence: `internal/cli/hook_lifecycle_cmd.go` renders `report` before
  checking `installErr`, although `hooks.Install` may return both on a partial
  failure.
- Ignored `json.MarshalIndent` results in fixed internal generators should be
  propagated while touching those paths, even where current values cannot
  encode-fail. Do not introduce panic-based helpers.
- Full icon decoding is intentionally out of scope. The documented MCP icon
  contract requires complete decode validation, not dimensions-only
  `image.DecodeConfig` validation.
- Verification passed: focused hook and CLI suites, race-enabled hook and CLI
  suites, Windows hook/CLI compilation, `make test-fast`, `make vet`,
  `make lint`, `make publication-audit`, `make self-host`, and
  `make test-release-trust`. The release-trust gate used disposable local
  artifacts only; no tag, push, or release was created.

## Deviations

None.
