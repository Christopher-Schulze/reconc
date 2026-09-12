# TASK 544: Replace unstable coverage policy classifier

## Why

The release-trust gate intermittently fails inside macOS awk on valid repository text. Retrying until green obscures the failure. The scanner also reports interpreter failures as policy violations.

## Acceptance

- One portable classifier owns the existing distinction between factual coverage measurements and numeric coverage requirements. Every existing positive, negative, negated-clause, malformed-input, and unreadable-file control retains its intended outcome.
- Classification does not depend on the macOS awk regex engine. Operational failures are distinguishable from detected policy and cannot produce a successful scan.
- The unchanged original failing input and a redistributable structural reduction pass repeated classification without retries or scan exclusions.
- No numeric coverage gate is introduced. Relevant tests, release trust, documentation, and the shared completion checks in TASK 555 pass.

## Sub-Tasks

- [x] Capture the reproduction, bounded reduction, interpreter version, input digest, and original classifier outcomes.
- [x] Move the classifier into the existing Go audit tooling and preserve clause semantics and scanner scope.
- [x] Add behavioral regression controls and distinguish policy findings from scanner failures.
- [x] Run focused controls and release trust locally; record evidence and complete the task lifecycle. Verify Linux against the pushed commit through hosted CI and retain final cross-platform qualification in TASK 555.

## Technical Plan

1. Inspect `verify_coverage_review_only` in `scripts/tests/release-trust.sh:157` and the caller near line 273 together with `scripts/audits/publication/main.go` and `audit.go`. Extract the present corpus before changing the classifier. Preserve mixed-clause ordering, bounded proximity, negation, comparisons, configuration notation, case, and punctuation.
2. Use Go's standard regexp engine with precompiled expressions or explicit clause checks, within the existing publication audit package. Extend its command contract only as needed for this gate; keep one implementation used by both fixtures and repository scanning. Invoke it once for the complete scan rather than starting a Go process per file.
3. Retain current file-selection semantics. Do not exclude Graphify, private task files, long lines, or invalid UTF-8 merely to hide this failure. Preserve invalid-byte handling explicitly and avoid a default Scanner token limit rejecting otherwise valid long input.
4. Separate a classified violation from an unreadable file, traversal error, parser failure, or canceled scan. Report the path and cause; the shell must propagate non-policy failures without the misleading numeric-policy message.
5. Add a deterministic synthetic long JSON-line fixture with the same relevant token structure. Do not commit private cache content. Compare the new classifier with the captured corpus, then run a bounded 1,000-call stress control and the full real repository scan. This demonstrates removal of the dependency on the failing engine; it does not prove a C-level awk root cause.

## Verification

Use real classifier inputs and the existing release-trust corpus. Run `go test ./scripts/audits/publication`, `make publication-audit`, and `make test-release-trust` after propagation. Cover empty files, CRLF, invalid bytes, long lines, adjacent negated and positive clauses, missing files, and injected traversal/read errors. A deliberately reintroduced requirement must fail; a forced I/O error must fail differently.

## Dependencies

None. Complete this before relying on full-gate results for the remaining tasks.

## Notes

Planning baseline: 2026-09-12, source `25ce22668769dbbb8c2360f8db6e7ecc574033e1`. The original 35,854-byte Graphify cache file is valid ASCII JSON with no NUL bytes. The unchanged classifier failed again at iteration 395 with exit 2; the small ASCII control passed 1,000 iterations. Evidence: `.build/planning-agent-integrations/awk-counted-reproduction.log`, `awk-reproduction.log`, and `awk-minimal-reproduction.log`. The failing cache path is `graphify-out/cache/ast/v0.8.47/6ce4369f0a479f09390889b8bf0931ed169e3b129f08a8c1c0189825dbaa44fd.json`. Logs are local evidence, not a required shipped fixture. The precise interpreter defect remains unproven.

Implementation evidence: the Go auditor's coverage-only mode scans the same current-tree extensions and root exclusions, reports policy rejection as exit 1 and operational failure as exit 2, and preserves LC_ALL=C byte/proximity semantics. Release trust retains its 41 original prose/syntax controls plus invalid-byte and missing-file checks, using one compiled auditor for the tree. Added regressions cover byte-distance boundaries, long lines, cancellation, output/read failure, root validation, and ignored/nested directories. The original input passed 1,000 consecutive checks with unchanged SHA-256 `b54e62748b001fa7885b204094ced0d433c20f406a3be67dbe5659acc08c3a7f`; evidence is in `.build/task544/original-input-stress.json`. The current 3,397-file tree scan passed.

Completion evidence (macOS arm64, Go 1.27.1): `make test` passed publication auditing, uncached race suites for both modules, and release trust with real isolated release artifacts (`.build/task544/full-test.log`, release target 87 seconds). `make vet`, `make lint`, and `make self-host` passed. The final focused publication race suite passed in 1.523 seconds. The synthetic long-record stress passed with `go test ./scripts/audits/publication -run '^TestCoverageReviewOnlyLongJSONLine$' -count=1000` in 19.508 seconds. Identical repetition runs separately from the ordinary regression suite; distinct long-line and denial assertions remain in that suite. The full suite had already compiled the earlier repeated test and also passed. Original private task history remains unchanged and excluded from publication. Hosted Linux results must be read for the exact pushed commit; this local completion record does not claim hosted success. TASK 555 owns the final integrated qualification.

## Deviations

Execution, task commits, and origin/main pushes were authorized after planning. Implementation uses a coverage-only mode in the existing publication auditor; its ordinary publication/history audit remains unchanged. Linux validation follows the task commit through hosted CI, since it cannot run natively on this macOS host. Final shared completion checks remain open under TASK 555.
