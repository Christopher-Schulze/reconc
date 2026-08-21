# TASK Control Plane

## Active

## Queue

- [ ] 194 Parse repository configuration once -> tasks/194-parse-repository-configuration-once.md
- [ ] 195 Compute source provenance once per compile -> tasks/195-compute-source-provenance-once-per-compile.md
- [ ] 196 Normalize rule JSON once per compiler boundary -> tasks/196-normalize-rule-json-once-per-compiler-boundary.md
- [ ] 197 Decode and validate each lockfile once -> tasks/197-decode-and-validate-each-lockfile-once.md
- [ ] 198 Reuse one stop-attempt lockfile scan -> tasks/198-reuse-one-stop-attempt-lockfile-scan.md
- [ ] 199 Reuse stable stop-policy snapshots within an attempt -> tasks/199-reuse-stable-stop-policy-snapshots-within-an-attempt.md
- [ ] 200 Recover hook workers after oversized frames -> tasks/200-recover-hook-workers-after-oversized-frames.md
- [ ] 201 Eliminate quadratic hook-worker frame assembly -> tasks/201-eliminate-quadratic-hook-worker-frame-assembly.md
- [ ] 202 Revalidate TASK path components across reads -> tasks/202-revalidate-control-path-components-across-reads.md
- [ ] 203 Make policy-source precedence truthful and canonical -> tasks/203-make-policy-source-precedence-truthful-and-canonical.md
- [ ] 204 Reject fields unsupported by a rule kind -> tasks/204-reject-fields-unsupported-by-a-rule-kind.md
- [ ] 205 Centralize and enforce template-variable grammar -> tasks/205-centralize-and-enforce-template-variable-grammar.md
- [ ] 206 Enforce parser cardinality and text limits -> tasks/206-enforce-parser-cardinality-and-text-limits.md
- [ ] 207 Avoid duplicate require-script batch preparation -> tasks/207-avoid-duplicate-require-script-batch-preparation.md
- [ ] 208 Memoize evidence matches within one policy evaluation -> tasks/208-memoize-evidence-matches-within-one-policy-evaluation.md
- [ ] 209 Expose one stable bounded assurance-file read -> tasks/209-expose-one-stable-bounded-assurance-file-read.md
- [ ] 210 Memoize package-manager ancestry detection -> tasks/210-memoize-package-manager-ancestry-detection.md
- [ ] 211 Reuse immutable action context roots -> tasks/211-reuse-immutable-action-context-roots.md
- [ ] 212 Validate immutable compiled action plans once -> tasks/212-validate-immutable-compiled-action-plans-once.md
- [ ] 213 Compute action argument size once per evaluation -> tasks/213-compute-action-argument-size-once-per-evaluation.md
- [ ] 214 Deduplicate runtime evidence in linear time -> tasks/214-deduplicate-runtime-evidence-in-linear-time.md
- [ ] 215 Enforce harness-pack limits before retaining payloads -> tasks/215-enforce-harness-pack-limits-before-retaining-payloads.md
- [ ] 216 Make context-size accounting bounded and overflow-safe -> tasks/216-make-context-size-accounting-bounded-and-overflow-safe.md
- [ ] 217 Make extracted rule identifiers collision-resistant -> tasks/217-make-extracted-rule-identifiers-collision-resistant.md
- [ ] 218 Close proof-bundle privacy inference gaps -> tasks/218-close-proof-bundle-privacy-inference-gaps.md
- [ ] 219 Report every review-relevant lockfile change -> tasks/219-report-every-review-relevant-lockfile-change.md

## Blocked

## Done

- [x] 193 Extract inline policy blocks linearly and within bounds -> tasks/done/193-extract-inline-policy-blocks-linearly-and-within-bounds.md
- [x] 192 Bind repository source reads to stable file identities -> tasks/done/192-bind-repository-source-reads-to-stable-file-identities.md
- [x] 191 Bound policy glob expansion before materialization -> tasks/done/191-bound-policy-glob-expansion-before-materialization.md
- [x] 190 Reuse one policy discovery and source-loading context -> tasks/done/190-reuse-one-policy-discovery-and-source-loading-context.md
- [x] 189 Batch write-epoch path normalization -> tasks/done/189-batch-write-epoch-path-normalization.md
- [x] 188 Batch prospective path identity resolution -> tasks/done/188-batch-prospective-path-identity-resolution.md
- [x] 187 Memoize stable evidence-file snapshots per evaluation -> tasks/done/187-memoize-stable-evidence-file-snapshots-per-evaluation.md
- [x] 186 Normalize command evidence once per evaluation -> tasks/done/186-normalize-command-evidence-once-per-evaluation.md
- [x] 185 Compile expected shell invocations once -> tasks/done/185-compile-expected-shell-invocations-once.md
- [x] 184 Precompile runtime template matchers -> tasks/done/184-precompile-runtime-template-matchers.md
- [x] 183 Precompile runtime path matchers -> tasks/done/183-precompile-runtime-path-matchers.md
- [x] 182 Preserve source freshness on runtime-plan cache hits -> tasks/done/182-preserve-source-freshness-on-runtime-plan-cache-hits.md
- [x] 181 Store audit evidence with a private JSONL layout -> tasks/done/181-store-audit-evidence-with-a-private-jsonl-layout.md
- [x] 180 Bound and cancel production file-lock acquisition -> tasks/done/180-bound-and-cancel-production-file-lock-acquisition.md
- [x] 179 Unify secure private state and lock publication -> tasks/done/179-unify-secure-private-state-and-lock-publication.md
- [x] 178 Verify bootstrap publications through open file identities -> tasks/done/178-verify-bootstrap-publications-through-open-file-identities.md
- [x] 177 Secure atomic publication parent identities -> tasks/done/177-secure-atomic-publication-parent-identities.md
