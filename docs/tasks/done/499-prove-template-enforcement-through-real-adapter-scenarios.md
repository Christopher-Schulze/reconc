# TASK 499: Prove template enforcement through real adapter scenarios

## Why

Evaluator-level template tests do not establish that generated host adapters select the right rule phase, encode the right decision or prevent the side effect. The review found gaps specifically at template-to-pre-hook and cache/evidence boundaries.

## Acceptance

- A shared bounded scenario corpus exercises actual template expansion, generated adapter routing, runtime decision, controlled side effect, post evidence and Stop.
- Supported pre-action guarantees are proven before a real filesystem/command effect; Stop-only detection is not labeled prevention.
- Host capability limitations, event/exit envelopes and permission behavior are explicit and tested rather than inferred from another adapter.
- Scenarios cover composite prevention, prior authorization, stale repeated tool IDs, long sessions and relevant new recipes without deferring each originating TASK's own regression tests.

## Sub-Tasks

- [x] Inventory the host route registry, generated adapters and existing fixture runners; map advertised template guarantees to supported event phases.
- [x] Define a small scenario representation with initial real repository state, exact event sequence, expected decisions/effects/evidence and capability applicability.
- [x] Reuse existing runners to execute generated artifacts and real Reconc boundaries in isolated repositories, including supported Bun adapter contracts.
- [x] Add cross-boundary scenarios for the completed fixes and verify failures against deliberately broken or pre-fix behavior where practical.
- [x] Integrate the corpus into existing fast/full/self-host gates with bounded runtime and update the host/template guarantee matrix in documentation.

## Notes

### Review provenance

- Review finding 30 from the 2026-09-08 source review at `a60196dc2f0954fd4f09d252fa65c709edfe4b01`.
- Status: implemented. The review established source-level evidence; this TASK reproduces the behavior at the template, generated-adapter and public runtime boundaries.
- Dependencies: Consumes the relevant completed behavior from TASK 470-498, especially TASK 471-474, TASK 480-481 and TASK 498. Originating TASKs retain their own mandatory regression coverage.

### Implementation evidence

- `internal/cli/hook_scenario_e2e_test.go` is the shared bounded corpus. It expands real built-in templates in isolated repositories, executes the public `hook runtime` boundary, performs controlled filesystem effects, checks persisted session evidence, and evaluates Stop.
- Native JSON adapters for Claude Code, Codex, GitHub Copilot, Cursor, Devin CLI, Antigravity, Grok, and ZCode execute their generated route command in a real test-binary child process. OpenCode, Kilo, Oh My Pi, and Pi are explicitly classified as Bun transports and remain covered by the existing executable Bun contract suite. Kimi Code is explicitly classified as global receipt-bound configuration and is not represented as local project execution.
- Host-specific decision contracts are asserted independently: exit-code blocking for native exit routes; Copilot `permissionDecision`, Cursor `permission`, Grok and Antigravity `decision` JSON; and unchanged target bytes after every denied pre-action.
- The corpus includes template prevention, Stop-only `require_script` detection, prior claim authorization, composite denial, changed-path reuse of a repeated tool ID, and 72-event evidence retention in one session.

### Source anchors

- `internal/runtime/builtin_templates_test.go`
- `internal/runtime/agentsession/handlers.go`
- `internal/runtime/agentsession/antigravity_test.go`
- `internal/hooks/testdata/host-events/antigravity.json`
- `internal/cli/hook_request_test.go`
- `harness/scaffold_policy_test.go`

### Technical design

- This TASK consolidates cross-host coverage; it does not replace or postpone focused regression tests in TASK 470-498.
- Use real temporary files, subprocesses and persisted evidence. Simulated host envelopes are acceptable protocol fixtures, but must not bypass the policy decision or effect under test.
- Model unsupported capabilities as explicit expectations; do not skip a promised guarantee to make the suite pass.
- Keep one shared scenario definition and host-specific envelope translation instead of copying logically identical tests into many files.

### Verification and completion

- For a denied write, assert target bytes and file membership are unchanged before and after the attempted routed action.
- For an allowed action, observe the real effect and verify its post evidence and final decision.
- Exercise malformed envelopes, duplicate/out-of-order events, path/source mutation between stages, cancellation and adapter-specific permission encoding.
- Record per-host coverage and unavailable native execution honestly; generated-code compilation alone is not an enforcement proof.
- Before Done, run the affected package tests and required repository gates (`make test-fast`, `make test`, `make vet`, `make lint`, `git diff --check`); include reference/publication checks when their owned artifacts change. Record actual commands and outcomes in this file.
- Keep changes scoped, preserve unrelated work, flush current behavior into `docs/documentation.md` where needed, then archive this detail and create the single TASK commit. Never push or change the product version without explicit authorization.
- Never run repository-targeted Reconc commands against this product root; integration validation belongs in isolated temporary repositories and `make self-host`.

### Actual verification

- `go test ./internal/cli -run 'TestHookTemplateScenarioCorpus|TestHookScenarioHostCapabilityMatrix|TestHookScenarioHostPreDenialUsesEachEnvelope|TestGeneratedAdaptersExecuteTemplateDenial|TestHookScenarioChild' -count=1` passed.
- `go test ./internal/cli -count=1` passed (43.329s); `go test ./... -count=1` exercised the complete module and hit the pre-existing timing-sensitive `TestFollowRunLogTailsNewRecords` timeout, whose isolated rerun passed.
- `make test-fast` passed with format/reference checks, root packages, and the portable harness template.
- `make test` passed with publication audit, uncached race suites, portable harness race suites, and release trust.
- `make vet`, `make lint`, `make self-host`, and `git diff --check` passed; reference docs and harness-pack checks were included by the required gates.
- Existing focused hook/runtime suites in the same gates cover malformed envelopes, duplicate/out-of-order handling and cancellation; this corpus adds path mutation and host-specific permission/exit assertions around the same real decision boundary.

## Deviations

None planned.
