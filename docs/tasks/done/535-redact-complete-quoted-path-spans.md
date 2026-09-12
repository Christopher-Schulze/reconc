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

- [x] Read sanitizer and verification boundaries and reproduce spaced-path suffix leaks in text fields.
- [x] Recognize complete quoted path spans with Unicode and path-appropriate escaping; conservatively redact ambiguous spans while preserving unrelated text and web URLs.
- [x] Keep structured and embedded path rules consistent without losing portable repository-relative identities.
- [x] Add platform-independent path matrices and idempotence regressions; update docs, run gates, archive, commit, and push origin/main.

## Notes

- Starting source: 70117feaa45b85d045670219d598fe9d1f1428e8.
- Source boundaries: internal/proofbundle/path.go; internal/proofbundle/bundle.go; internal/proofbundle/verify_contract.go.
- Use isolated repository fixtures; never run repository-targeted Reconc commands against the product root.
- Planning details are explicitly tracked for this approved work despite the existing local-task ignore rule.
- Regression matrices reproduced suffix leaks across quoted path dialects, URI host leakage, and home-prefix replacement. Quote tracking now retains the complete span, honors escaped closing quotes, and conservatively consumes unterminated spans.
- File URIs also reproduced a structured-path inconsistency: normalization could turn them into apparently portable identities. Sanitization now emits the external marker and verification rejects file-URI path fields.
- Existing repository-relative identity and ordinary web-URL controls remain green; each new text case verifies stable repeated sanitization.
- Validation passed: complete proof-bundle package under race detection, all final focused regressions, make test (both uncached race suites and release trust), make vet, make lint, make build, and git diff --check. Logs: .build/review-repairs/task535-*.log.
- The preceding remote CI exposed two inherited Gitlink fixture failures on Linux (missing author identity in cloned submodules). Their isolated reproduction and repair belong to TASK 538; this task does not claim remote CI success.

## Deviations

None.
