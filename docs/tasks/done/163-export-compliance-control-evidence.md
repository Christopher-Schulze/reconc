# TASK 163: Export compliance-ready control evidence

## Why

The action ledger, policy lock, scenarios, approvals, and budget state can supply
useful audit evidence, but claiming certification or automatic legal compliance
would be false. Operators need a deterministic local export that maps actual
Reconc evidence to named control objectives, discloses missing evidence, and
stays usable by external GRC workflows without a hosted Reconc service.

## Acceptance

- A versioned control-map format links stable control identifiers to exact Reconc
  policy, lock, scenario, ledger, approval, budget, integrity, and completeness
  fields without embedding unverifiable prose claims in runtime events.
- Built-in mappings cover only control objectives supported by current primary
  sources for SOC 2, GDPR, HIPAA, and the EU AI Act at implementation time. Each
  mapping records source edition/date, evidence rationale, known gaps, and review
  status.
- Output language says `evidence`, `mapping`, `covered`, `partial`, `missing`, or
  `not evaluated`; it never says certified, compliant, guaranteed, approved by a
  regulator, or legally sufficient.
- Export verifies policy-lock identity, action-ledger retained-chain integrity,
  approval receipt validity, scenario results, budget-state identity, evidence
  window, and archive completeness before assigning status.
- Missing archives, broken chains, stale policy, incomplete scenario classes,
  unsupported host coverage, untrusted identity, absent approval authority, or
  unavailable budget evidence lowers the affected control status explicitly.
- JSON and Markdown exports are deterministic, bounded, local, offline, and
  privacy-preserving. They contain safe identifiers, digests, counts, categories,
  coverage, and gaps, not raw arguments, tool results, secrets, or personal data.
- User-supplied mapping packs are strict, signed or content-digest-pinned,
  versioned, schema-validated, and cannot override built-in evidence facts or
  transform missing evidence into covered evidence.
- `reconc action evidence export` and `verify` are command-registry-owned and
  expose exact evidence windows, mapping pack identity, retained-history bounds,
  and completeness in text and JSON.
- Export tests include fully covered synthetic evidence, partial evidence,
  missing data, tamper, stale mapping, source-version change, privacy leakage,
  deterministic ordering, oversized packs, and malicious descriptions.
- Documentation explains the difference between technical evidence generation,
  an organization's control design and operation, legal assessment, and external
  certification.
- No network call, account, telemetry, PDF generator, proprietary format, or
  cloud dashboard is required.

## Sub-Tasks

- [x] Verify current primary control sources and licensing/quotation limits for
      each proposed framework before writing mappings
- [x] Define control-map schema, stable IDs, source metadata, evidence selectors,
      rationale, gaps, review status, and pack identity
- [x] Define the evidence-status lattice so missing or incomplete facts can never
      be promoted by user prose or mapping priority
- [x] Map exact action policy, lock, scenario, ledger, approval, budget,
      integrity, retention, and coverage fields into the evidence model
- [x] Define evidence-window and retained-history semantics across rotated action
      ledgers and policy changes
- [x] Validate built-in mapping statements against primary sources and keep
      quotations within permitted bounds
- [x] Load optional mapping packs through strict bounded regular-file and schema
      checks with content-digest or signature identity
- [x] Prevent custom packs from overriding evidence integrity, completeness,
      provenance, or missing-state facts
- [x] Implement deterministic local JSON and Markdown renderers with bounded
      safe text and stable ordering
- [x] Register `action evidence export` and `verify` in command metadata before
      CLI dispatch and documentation
- [x] Include mapping version, source edition/date, policy and lock digests,
      ledger boundaries, scenario identity, export time basis, coverage, and gaps
- [x] Add privacy scans proving synthetic raw arguments, results, secrets,
      headers, credentials, and personal data never enter exports
- [x] Add synthetic complete, partial, missing, tampered, stale, rotated,
      unsupported-host, and untrusted-authority fixtures
- [x] Add deterministic-output, schema, pack-signature/digest, malicious-text,
      oversized-input, archive-gap, and source-version tests
- [x] Add mutation tests proving every integrity, completeness, provenance, and
      gap downgrade affects the expected control status
- [x] Update RFCs, schemas, architecture, documentation, commands, retention,
      privacy, release inventory, and publication audits
- [x] Add explicit non-certification and legal-review language to every user
      surface that mentions a framework
- [x] Re-read every modified file and run focused tests, complete module gates,
      static analysis, coverage, and publication audits

## Notes

Depends on TASK 160 and uses TASK 156 scenario completeness, TASK 158 approval
evidence, and TASK 157 budget evidence. It does not block the core gateway and
can be omitted from an earlier release if the user explicitly reschedules it.

This is an evidence exporter, not legal advice or a certification product.
Framework mappings are versioned data with review dates because source language
and regulatory guidance can change.

Primary-source review used the AICPA 2017 Trust Services Criteria with revised
2022 points of focus, consolidated GDPR text, current 45 CFR Part 164 Subpart C,
and consolidated Regulation EU 2024/1689. Built-ins store identifiers and
original Reconc paraphrases only; no source quotation is embedded.

Final hardening added exact current-evaluator scenario replay, full approval
receipt cryptographic reverification, fail-closed state and ledger resampling,
canonical UTC and retained-record bounds, exact report/status reconstruction,
privacy scanning, four registry-owned public contracts, and real release-asset
coverage. Final gates passed: `make test`, `make vet lint`, serial
`make coverage`, `make self-host`, focused race tests, `git diff --check`, and
Go formatting. Coverage measured 81.9398% for the root module and 83.5729% for
the portable template module; coverage remains review evidence, not a numeric
acceptance threshold.

## Deviations

None.
