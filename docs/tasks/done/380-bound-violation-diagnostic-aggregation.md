# TASK 380: Bound violation diagnostic aggregation

## Why

Script and assurance failures are joined across every matching context into `Violation.Explanation` and remediation text without a final byte ceiling. The deep doctor path also embeds up to 4 MiB of failed `grok inspect --json` stdout verbatim in one diagnostic. Bounded inputs can therefore amplify into oversized JSON, terminal, and CI artifacts, and external output can leak into shareable diagnostics.

## Acceptance

- Every violation text field has an explicit UTF-8-safe byte limit.
- External-command details embedded in doctor output have a small UTF-8-safe ceiling and an explicit truncation marker.
- Truncation preserves the first actionable failures plus an exact omitted-count marker.
- Decision, matched-path, required-evidence, and exit-code semantics remain unchanged.
- Adversarial regressions cover maximum contexts, maximum script output, multi-byte text, and serialization limits.

## Sub-Tasks

- [x] Inventory every violation and external-command diagnostic producer plus downstream artifact limits.
- [x] Add one deterministic bounded aggregation contract.
- [x] Add adversarial script, assurance, doctor stdout, JSON, and CLI output tests.
- [x] Run focused runtime and reporting tests.

## Notes

- Verified from finding 11 and worker findings 736, 840, and 861.
- The script paths append one failure string per context, then join the complete slice into explanation and remediation fields.
- `doctorCheckGrokRuntime` receives output bounded at `doctorGrokInspectMaxBytes` and appends the complete body on command failure; the oversize branch cannot protect any body already admitted at that same limit.
- `Violation.Message`, `Explanation`, and `RecommendedAction` now share a 16 KiB UTF-8 byte boundary. Multi-context aggregates retain the first actionable details within 8 KiB and append an exact omitted-count marker; matched and required evidence slices remain exact.
- Failed Grok inspection details now retain at most 1 KiB of normalized error text inside one 4 KiB single-line UTF-8 diagnostic with an explicit truncation marker.
- Adversarial regressions cover 262,144 contexts, maximum script output, 50 assurance findings, invalid and multibyte UTF-8, staged CI hint propagation, doctor terminal controls, JSON serialization, and unchanged decision/evidence semantics.
- Focused runtime, CLI, CI-report, proof, policy-proof, and completion-gate package tests passed uncached. `make test-fast`, reference-doc checks, formatting checks, and the portable-template suite passed.

## Deviations
