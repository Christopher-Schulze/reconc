# TASK Control Plane

## Active

## Queue

- [ ] 295 Durably create and rotate JSONL state -> tasks/295-durably-create-and-rotate-jsonl-state.md
- [ ] 296 Sync repository-run state mutations before unlock -> tasks/296-sync-repository-run-state-mutations-before-unlock.md
- [ ] 297 Bind runtime freshness metadata to the opened file -> tasks/297-bind-runtime-freshness-metadata-to-the-opened-file.md
- [ ] 298 Propagate caller cancellation into policy scripts -> tasks/298-propagate-caller-cancellation-into-policy-scripts.md
- [ ] 299 Prevent symlink side effects in create-capable file opens -> tasks/299-prevent-symlink-side-effects-in-create-capable-file-opens.md
- [ ] 300 Apply Windows private ACLs through opened handles -> tasks/300-apply-windows-private-acls-through-opened-handles.md
- [ ] 301 Sanitize every proof and agent-session Git environment -> tasks/301-sanitize-every-proof-and-agent-session-git-environment.md
- [ ] 302 Route all untrusted YAML through bounded admission -> tasks/302-route-all-untrusted-yaml-through-bounded-admission.md
- [ ] 303 Enforce the expanded YAML alias budget -> tasks/303-enforce-the-expanded-yaml-alias-budget.md
- [ ] 304 Fix assurance guard comment classification -> tasks/304-fix-assurance-guard-comment-classification.md
- [ ] 305 Reject extension-only policy targets -> tasks/305-reject-extension-only-policy-targets.md
- [ ] 306 Allow safe double dots in include patterns -> tasks/306-allow-safe-double-dots-in-include-patterns.md
- [ ] 307 Make malformed-config backup identities collision-resistant -> tasks/307-make-malformed-config-backup-identities-collision-resistant.md
- [ ] 308 Parse Codex activation with complete TOML semantics -> tasks/308-parse-codex-activation-with-complete-toml-semantics.md
- [ ] 309 Make hook install authorization atomic with publication -> tasks/309-make-hook-install-authorization-atomic-with-publication.md
- [ ] 310 Derive proof-bundle executable identity from shell syntax -> tasks/310-derive-proof-bundle-executable-identity-from-shell-syntax.md
- [ ] 311 Correct action trace byte accounting -> tasks/311-correct-action-trace-byte-accounting.md
- [ ] 312 Use canonical size for budget arguments -> tasks/312-use-canonical-size-for-budget-arguments.md
- [ ] 313 Eliminate redundant compiler payload serialization -> tasks/313-eliminate-redundant-compiler-payload-serialization.md
- [ ] 314 Remove the discarded action-ledger checkpoint decode -> tasks/314-remove-the-discarded-action-ledger-checkpoint-decode.md
- [ ] 315 Check forbid-command path triggers before command analysis -> tasks/315-check-forbid-command-path-triggers-before-command-analysis.md
- [ ] 316 Eliminate logical-condition child allocations -> tasks/316-eliminate-logical-condition-child-allocations.md
- [ ] 317 Reduce decimal parse and render allocation -> tasks/317-reduce-decimal-parse-and-render-allocation.md
- [ ] 318 Avoid repeated glob-expansion key construction -> tasks/318-avoid-repeated-glob-expansion-key-construction.md
- [ ] 319 Implement the completion-state retry contract -> tasks/319-implement-the-completion-state-retry-contract.md
- [ ] 320 Close the completion-proof publication mutation window -> tasks/320-close-the-completion-proof-publication-mutation-window.md
- [ ] 321 Capture coherent command-proof Git state -> tasks/321-capture-coherent-command-proof-git-state.md
- [ ] 322 Remove duplicate TASK move precondition validation -> tasks/322-remove-duplicate-move-precondition-validation.md
- [ ] 323 Replace audit error-string protocols with typed classification -> tasks/323-replace-audit-error-string-protocols-with-typed-classification.md
- [ ] 324 Stream audit archive verification -> tasks/324-stream-audit-archive-verification.md
- [ ] 325 Retain bootstrap receipt-plan pairs atomically -> tasks/325-retain-bootstrap-receipt-plan-pairs-atomically.md
- [ ] 326 Surface repeated-stop state mutation failures -> tasks/326-surface-repeated-stop-state-mutation-failures.md
- [ ] 327 Reuse shell parser state safely -> tasks/327-reuse-shell-parser-state-safely.md
- [ ] 328 Cache Git alias discovery within one evaluation -> tasks/328-cache-git-alias-discovery-within-one-evaluation.md
- [ ] 329 Linearize session-state normalization -> tasks/329-linearize-session-state-normalization.md
- [ ] 330 Avoid duplicate full session-state equality passes -> tasks/330-avoid-duplicate-full-session-state-equality-passes.md
- [ ] 331 Reuse one runtime evaluator across MCP gateway checks -> tasks/331-reuse-one-runtime-evaluator-across-mcp-gateway-checks.md
- [ ] 332 Share pre-decision identity work within one hook event -> tasks/332-share-pre-decision-identity-work-within-one-hook-event.md
- [ ] 333 Index immutable schema contracts for direct lookup -> tasks/333-index-immutable-schema-contracts-for-direct-lookup.md
- [ ] 334 Propagate protocol and identity serialization failures -> tasks/334-propagate-protocol-and-identity-serialization-failures.md
- [ ] 335 Pin JSONL backup source identity before linking -> tasks/335-pin-jsonl-backup-source-identity-before-linking.md
- [ ] 336 Root bootstrap publication against path replacement -> tasks/336-root-bootstrap-publication-against-path-replacement.md
- [ ] 337 Report atomic-file post-publication outcomes truthfully -> tasks/337-report-atomic-file-post-publication-outcomes-truthfully.md
- [ ] 338 Parse custom-runtime manifests once -> tasks/338-parse-custom-runtime-manifests-once.md
- [ ] 339 Reuse canonical bytes across proof serialization -> tasks/339-reuse-canonical-bytes-across-proof-serialization.md
- [ ] 340 Centralize archive-ring bounds and symlink accounting -> tasks/340-centralize-archive-ring-bounds-and-symlink-accounting.md
- [ ] 341 Remove duplicate JSONL lock validation -> tasks/341-remove-duplicate-jsonl-lock-validation.md

## Blocked

- [!] 293 Make Windows atomic replacement genuinely write-through -> tasks/293-make-windows-atomic-replacement-genuinely-write-through.md

## Done

- [x] 294 Durably publish bootstrap plans and artifacts -> tasks/done/294-durably-publish-bootstrap-plans-and-artifacts.md
- [x] 292 Narrow MCP pre-dispatch serialization and retry policy -> tasks/done/292-narrow-mcp-pre-dispatch-serialization-and-retry-policy.md
- [x] 291 Continue MCP shutdown finalization after individual errors -> tasks/done/291-continue-mcp-shutdown-finalization-after-individual-errors.md
- [x] 290 Finalize pending approvals after call-drain timeout -> tasks/done/290-finalize-pending-approvals-after-call-drain-timeout.md
- [x] 289 Preserve MCP indeterminate-transition failures -> tasks/done/289-preserve-mcp-indeterminate-transition-failures.md
- [x] 288 Finish and prove the SWE audit repairs -> tasks/done/288-finish-and-prove-swe-audit-repairs.md
- [x] 287 Repair update isolation and replace v0.9.7 -> tasks/done/287-repair-update-isolation-and-replace-v0-9-7.md
- [x] 286 Eliminate open CodeQL findings -> tasks/done/286-eliminate-open-codeql-findings.md
- [x] 285 Stabilize the Windows MCP lifecycle release gate -> tasks/done/285-stabilize-windows-mcp-lifecycle-release-gate.md
- [x] 284 Repair Windows release-gate portability -> tasks/done/284-repair-windows-release-gate-portability.md
