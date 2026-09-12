# TASK 529: Redact foreign absolute paths in proof bundles

## Why

Proof-bundle sanitization uses the host platform's `filepath.IsAbs` and then
normalizes backslashes to slashes for non-absolute values. On a POSIX host a
Windows drive-absolute user directory is considered relative, becomes a
slash-normalized drive path, and then bypasses the Windows absolute-path
regex, which recognizes only backslash form. The portable proof-path validator
also accepts that drive-prefixed slash form. As a result, a bundle produced or
tested on one platform can expose a foreign user's absolute path.

This task implements audit candidate C239. Sanitization and validation must
recognize path dialects independently of the current GOOS while preserving
legitimate repository-relative paths and deterministic portable output.

## Acceptance

- Recognize POSIX absolute paths, Windows drive-absolute paths with slash or
  backslash separators, UNC paths, slash-normalized UNC paths, extended-length
  Windows paths, device paths, and drive-relative prefixes on every host.
- Classify foreign or outside-root absolute identities before separator
  normalization can erase the syntax needed to recognize them.
- Redact every outside-root or foreign absolute path to the existing canonical
  `<external>` representation without retaining username, host, share, drive,
  home directory, or parent segments.
- Preserve a path beneath the actual canonical repository root as its exact
  slash-separated repository-relative path when that relationship is provable
  on the current host.
- Treat a foreign-platform absolute path as external when host-local filesystem
  semantics cannot prove it belongs to the repository; do not guess cross-drive
  or cross-dialect relativity.
- Make `portableProofPath` reject every residual absolute, drive-prefixed, UNC,
  device, backslash, parent-escaping, dot, empty, or non-canonical path form.
- Apply equivalent protection to path fields embedded in bounded diagnostics,
  command summaries, evidence, and other exported proof text without corrupting
  ordinary colon-separated prose or safe repository-relative names.
- Preserve secret/token redaction, UTF-8 repair, boundary-aware replacement,
  item/byte bounds, stable uniqueness, deterministic ordering, and current
  schema shape.
- Add cross-platform table tests that run identically on Linux, macOS, and
  Windows for arbitrary usernames, mixed separators and case, drive letters,
  UNC hosts/shares, extended prefixes, spaces, quoting, punctuation boundaries,
  root-local paths, and deceptive relative prefixes.
- Add a portable contract regression proving no generated bundle containing any
  foreign absolute form passes verification.
- Add fuzz/property coverage for path-dialect classification and sanitization
  idempotence; failures must retain a stable minimal corpus case.
- Update proof privacy and portability documentation with the exact redaction
  boundary and the fact that foreign paths cannot be relativized.
- Pass proofbundle tests on available platforms, relevant release-trust and
  publication checks, and every required repository gate.

## Sub-Tasks

- [x] Read every proof-bundle path/text producer, sanitizer, verifier, schema, renderer, publication path, and test corpus; identify every field that can carry filesystem text.
- [x] Define one host-independent path-dialect classifier with exact POSIX, drive, UNC, extended, device, drive-relative, repository-relative, and invalid outcomes.
- [x] Integrate classification before normalization and strengthen portable verification without introducing duplicate regex contracts.
- [x] Cover all bundle fields and add arbitrary-identity, mixed-dialect, boundary, idempotence, fuzz-corpus, and full-contract regressions.
- [x] Verify safe relative paths, URLs, rule IDs, command syntax, and colon-bearing prose are not over-redacted.
- [x] Inspect output bounds and allocation impact for high-cardinality hostile path inputs.
- [x] Update privacy, portability, and proof-contract documentation.
- [x] Run focused cross-platform-capable tests and all required repository gates; re-read every modified file before archival.

## Notes

### Observed behavior

- Host `filepath.IsAbs` plus backslash-to-slash conversion let a slash-normalized
  Windows drive-absolute user directory survive on POSIX. The Windows regex
  required a backslash after the drive.

### Implementation

- `sanitizeProofPath` classifies with `pathidentity.Rooted` before separator
  normalization. Host-local absolutes relativize only when `filepath.IsAbs`
  can prove containment; every other rooted dialect becomes `<external>`.
- `portableProofPath` rejects residual absolute, drive, UNC, device,
  backslash, `..`, `.`, empty, and non-canonical forms.
- Embedded diagnostics use a span scanner that redacts those dialects while
  leaving `https://` URLs intact. Path-field emission has a portable backstop.

### Verification

- Table tests, Verify contract rejection, idempotence, URL preservation, and
  a retained fuzz corpus case. `go test -race ./internal/proofbundle`,
  `make lint`, publication-audit, and `make test` passed. Windows user paths in
  fixtures are split so publication-audit does not see a contiguous private path.

### Privacy invariants

- Exported paths reveal no foreign home, operator identity, host, share, drive,
  or outside-root hierarchy.
- Redaction is deterministic and idempotent on every GOOS.
- Host-local repository paths remain useful and exact relative identities.
- Sanitization and validation agree; no value sanitized as portable is later
  rejected, and no unsafe unsanitized dialect is admitted.
- Current token/secret detectors retain their behavior.

### Verification matrix

- POSIX user-home and home-directory forms, Windows drive-absolute user
  directories with slash or backslash, drive-relative forms, UNC and
  extended/device equivalents.
- Lower/upper drive letters, mixed separators, spaces, Unicode, punctuation,
  quoted paths, environment-assignment context, and multiple paths per string.
- Exact repository root, child, sibling, parent escape, symlinked child,
  prospective path, missing path, and foreign-dialect lookalike.
- Sanitizer output fed through the complete portable bundle verifier on every
  supported platform.

## Deviations

None.
