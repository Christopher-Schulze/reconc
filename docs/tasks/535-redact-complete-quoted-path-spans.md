# TASK 535: Redact complete quoted path spans

## Why

Text path redaction stops at spaces and leaves identifying suffixes from quoted Windows drive, UNC, and POSIX paths.

User-approved review item: R4. Execute on main; commit and push after completion. Product tags and releases remain outside this task.

## Acceptance

- Quoted external paths containing spaces leave no private path suffixes.
- Drive, UNC, POSIX, file-URI, punctuation, multiple-path, and ordinary web-URL cases are covered.
- Repeated sanitization is idempotent and bundle verification rejects nonportable path identities.
- Proof-bundle regressions and repository gates pass.

## Sub-Tasks

- [ ] Read sanitizer and verification boundaries and reproduce spaced-path suffix leaks in text fields.
- [ ] Recognize complete quoted path spans with Unicode and path-appropriate escaping; conservatively redact ambiguous spans while preserving unrelated text and web URLs.
- [ ] Keep structured and embedded path rules consistent without losing portable repository-relative identities.
- [ ] Add platform-independent path matrices and idempotence regressions; update docs, run gates, archive, commit, and push origin/main.

## Notes

- Starting source: 70117feaa45b85d045670219d598fe9d1f1428e8.
- Source boundaries: internal/proofbundle/path.go; internal/proofbundle/bundle.go; internal/proofbundle/verify_contract.go.
- Use isolated repository fixtures; never run repository-targeted Reconc commands against the product root.
- Planning details are explicitly tracked for this approved work despite the existing local-task ignore rule.

## Deviations

None.
