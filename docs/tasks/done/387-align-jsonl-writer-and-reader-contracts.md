# TASK 387: Align JSONL writer and reader contracts

## Why

The generic JSONL writer accepts empty records and records containing embedded line breaks, violating the reader's non-empty one-record-per-line contract. The audit writer also accepts a custom live-file cap larger than the fixed cap enforced by every audit reader.

## Acceptance

- JSONL append accepts exactly one non-empty record with no embedded CR or LF and writes one terminal newline.
- Audit append rejects or safely constrains writer sizes that its readers cannot consume.
- Existing canonical JSON records and rotation behavior remain unchanged.
- Adversarial tests cover empty/whitespace records, CR, LF, CRLF, oversized writer policies, rotation, and strict tail reads.

## Sub-Tasks

- [x] Formalize writer/reader framing and size invariants.
- [x] Enforce record shape before lock acquisition or mutation.
- [x] Align audit size configuration with the reader ceiling.
- [x] Run focused JSONL and audit tests.

## Notes

- Verified from findings 28, 29, and 151.
- Production callers marshal JSON, but the exported writer currently permits forged multi-record input; audit readers always use `DefaultMaxSizeBytes`.
- Reverification confirmed all three findings. `normalizeRecord` previously collapsed arbitrary trailing CR/LF runs, accepted blank input, and retained embedded delimiters; `audit.Append` accepted a live cap above every reader's fixed 2 MiB snapshot limit; `decodeAuditFile` silently skipped empty records.
- Generic append now accepts one non-whitespace payload, optionally terminated by exactly one LF or CRLF, rejects remaining CR/LF before direct-append lock acquisition and before transactional mutation, and emits one LF without mutating caller bytes.
- Audit append rejects a custom cap above `DefaultMaxSizeBytes` before creating lock, live, journal, or detached-head state. The reader rejects empty records with exact source and line context.
- Canonical JSON callers in agent sessions, action ledger, and audit marshal newline-free records and remain unchanged.
- Focused gate: `go test ./internal/jsonl ./internal/audit ./internal/actionledger ./internal/runtime/agentsession -count=1` passed.
- Repository fast gate: `make test-fast` passed, including the root module and portable harness template.

## Deviations
