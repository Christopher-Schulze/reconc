# TASK 557: Close DSH execution and provider policy gaps

## Why

Audit findings 1-3 expose unclassified execution routes, persistent Bash state misbinding, and external child runtimes without inherited protection. Finding 8 requires durable offline regressions; finding 9 requires accurate source/offline acceptance documentation.

## Acceptance

- Native `run_code`, raw terminal execution, and PowerShell fail closed with useful alternatives at the extension and Go normalization boundaries.
- Persistent Bash is identified from the active Cordis provider rather than accepted as stateless Bash.
- Delegation through official configurable tools accepts known in-process providers and rejects unbound external providers, including renamed delegation tools and late configuration changes.
- Existing read/write/edit/Bash, classified custom tools, guarded in-process children, and Stop contracts remain working.
- Source-bound offline regressions exercise each denied route and allowed counterpart; current docs accurately describe supported profiles without requiring host/model runs.

## Sub-Tasks

- [x] Bind source contracts to execution routes and active provider configurations; implement explicit unsupported-route rejection.
- [x] Add extension and real Go policy regressions, including late provider changes and renamed tools.
- [x] Propagate support boundaries into canonical docs and verify related tests/build before archiving.

## Notes

Official source inspected temporarily at upstream commit `c291e7961a515f6d7af9304e7fd1d257929aef26`, compared with release `dsh-v0.1.5-rc.2` (`fb2c4b9e698e30edb738bca4cf0618587db7d203`). Relevant contracts: `vendor/cordis/src/registry.ts` and `fiber.ts`, `packages/core/tools/src/index.ts`, `packages/subagent/`, `packages/workflow/`, `packages/shell/tool-bash-persistent/`, and `packages/terminal/tool-terminal/`. No DSH installation or host execution is required or authorized. Arbitrary JavaScript and persistent REPL input cannot be soundly checked by the Bash parser; deny those routes with native-tool alternatives. External providers are not treated as protected merely because a patch file exists. Provider configuration is rechecked at the final guard.

## Deviations

Verification: full `go test -p=4 ./...`, targeted generated-extension and scaffold parity tests, `make build`, `make vet`, and `make lint` passed. The source-backed tests preserve native allow paths while covering unsafe tools, persistent Bash, renamed delegation, external provider selection, final-guard configuration drift, and workflow/Ralph routing. Documentation now defines source/offline acceptance.

The generated scaffold and deterministic advanced pack must carry the same extension. The scaffold was patched surgically. The pack generator published only after a temporary manifest diff and ZIP integrity check; both committed artifacts matched the reviewed candidate byte for byte. The CLI test helper captures writers but does not render returned CLI errors, so diagnostics are asserted at normalization and denial at the CLI boundary.
