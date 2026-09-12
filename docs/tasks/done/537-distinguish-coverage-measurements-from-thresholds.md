# TASK 537: Distinguish coverage measurements from thresholds

## Why

The release-trust classifier rejects factual numerical measurements as minimum-coverage policy. TASK 525 historical measurements were removed to bypass this false positive.

User-approved review item: R6. Execute on main; commit and push after completion. Product tags and releases remain outside this task.

## Acceptance

- Historical numeric measurement and descriptive evidence pass the classifier.
- Explicit minimum requirements and threshold configuration remain rejected.
- Removed TASK 525 measurements are restored without presenting them as current results.
- Classifier regression fixtures and required gates pass.

## Sub-Tasks

- [x] Read the release-trust coverage classifier and the TASK 525 historical diff; reproduce measurement, threshold, and descriptive controls.
- [x] Restrict rejection to normative coverage requirements and threshold configuration while admitting factual historical measurements.
- [x] Add positive and negative classifier fixtures that verify the actual distinction instead of suppressing the test.
- [x] Restore the historical TASK 525 values with their original source commit and explicitly historical scope.
- [x] Run release-trust and repository gates, preserve current measurement provenance, archive, commit, and push origin/main.

## Notes

- Starting source: 70117feaa45b85d045670219d598fe9d1f1428e8.
- Source boundaries: scripts/tests/release-trust.sh; docs/tasks/done/525-make-pre-decision-caching-sound-and-efficient.md.
- Use isolated repository fixtures; never run repository-targeted Reconc commands against the product root.
- Planning details are explicitly tracked for this approved work despite the existing local-task ignore rule.
- The prepared classifier passes seven factual/descriptive controls and rejects twelve explicit requirements, threshold assignments, and numeric comparison controls. Rejection requires policy wording, threshold configuration, or comparison syntax rather than mere proximity to a numerical measurement. Read errors propagate as failures.
- Restore measurements from the archived TASK 525 text at `cb71a3b7a33bd651da95875632dfbdb422ef4aff`: root 82.3484%, portable template 84.0628%. These are historical measurements, not current results or acceptance thresholds.
- Verification passed: all nineteen embedded classifier controls, full release trust, `make test-fast` for both complete modules, `make vet`, `make lint`, `make build`, shell syntax, and `git diff --check`. The preceding TASK 536 complete race gate covers unchanged Go implementation; TASK 538 performs the final integrated uncached gate. Logs: `.build/review-repairs/task537-*.log`.
- ShellCheck passes the exact extracted production classifier and fixtures. Whole-file ShellCheck reports the same three pre-existing warnings (SC1007, SC2097, SC2098) at the deliberate empty-environment/explicit-Make release-tag invocation as the parent revision; this task does not claim whole-file ShellCheck success or alter that unrelated fixture.
- The old classifier reproduction rejects the historical measurement, while the new full project scan retains it. Documentation now describes the distinction between measurements and pass/fail contracts.

## Deviations

None.
