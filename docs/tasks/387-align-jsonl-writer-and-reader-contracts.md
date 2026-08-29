# TASK 387: Align JSONL writer and reader contracts

## Why

The generic JSONL writer accepts empty records and records containing embedded line breaks, violating the reader's non-empty one-record-per-line contract. The audit writer also accepts a custom live-file cap larger than the fixed cap enforced by every audit reader.

## Acceptance

- JSONL append accepts exactly one non-empty record with no embedded CR or LF and writes one terminal newline.
- Audit append rejects or safely constrains writer sizes that its readers cannot consume.
- Existing canonical JSON records and rotation behavior remain unchanged.
- Adversarial tests cover empty/whitespace records, CR, LF, CRLF, oversized writer policies, rotation, and strict tail reads.

## Sub-Tasks

- [ ] Formalize writer/reader framing and size invariants.
- [ ] Enforce record shape before lock acquisition or mutation.
- [ ] Align audit size configuration with the reader ceiling.
- [ ] Run focused JSONL and audit tests.

## Notes

- Verified from findings 28, 29, and 151.
- Production callers marshal JSON, but the exported writer currently permits forged multi-record input; audit readers always use `DefaultMaxSizeBytes`.

## Deviations
