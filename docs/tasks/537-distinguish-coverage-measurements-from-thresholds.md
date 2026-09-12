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

- [ ] Read the release-trust coverage classifier and the TASK 525 historical diff; reproduce measurement, threshold, and descriptive controls.
- [ ] Restrict rejection to normative coverage requirements and threshold configuration while admitting factual historical measurements.
- [ ] Add positive and negative classifier fixtures that verify the actual distinction instead of suppressing the test.
- [ ] Restore the historical TASK 525 values with their original source commit and explicitly historical scope.
- [ ] Run release-trust and repository gates, preserve current measurement provenance, archive, commit, and push origin/main.

## Notes

- Starting source: 70117feaa45b85d045670219d598fe9d1f1428e8.
- Source boundaries: scripts/tests/release-trust.sh; docs/tasks/done/525-make-pre-decision-caching-sound-and-efficient.md.
- Use isolated repository fixtures; never run repository-targeted Reconc commands against the product root.
- Planning details are explicitly tracked for this approved work despite the existing local-task ignore rule.

## Deviations

None.
