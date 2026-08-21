# TASK 218: Close proof-bundle privacy inference gaps

## Why

Proof bundles intentionally exclude raw command arguments and absolute paths,
but they publish an unsalted SHA-256 of the normalized full command. A holder
can test guesses for short or conventional commands even though the visible
command is summarized. Text sanitization also recognizes only a finite token
set, and its per-call dynamic repository/user regex construction adds repeated
work without a single documented privacy test corpus.

## Acceptance

- The public command-proof identity has an explicit privacy model and cannot be
  used as an offline oracle for raw arguments; deterministic review needs are
  met with a non-sensitive identity or the field is removed through a schema
  migration.
- Sanitization covers assignment quoting and representative provider-token/path
  forms through a maintained adversarial corpus while acknowledging that regex
  redaction is defense in depth, not secret discovery.
- Repository/home/user replacement is boundary-aware and cannot expose sibling
  paths through partial string substitution.
- Sanitized output remains portable, bounded, valid UTF-8, deterministic where
  required, and accepted by the verifier.
- Tests prove no raw arguments, absolute paths, usernames, or corpus tokens
  survive in JSON or Markdown bundles.

## Sub-Tasks

- [x] Define the public proof privacy and determinism contract
- [x] Replace the guessable raw-command hash safely
- [x] Centralize boundary-aware sanitization and its corpus
- [x] Align schema, verifier, JSON, and Markdown rendering
- [x] Run proof, privacy, fuzz, and complete gates

## Notes

- Verified in `commandProofs`, `sanitizeText`, and `hashString` in
  `internal/proofbundle/bundle.go`.
- `<external>` and the bounded-text marker are intentional current contract
  values; the old session's claims that they independently bypass verification
  were rejected.
- `CommandHash` now hashes only the sanitized executable identity. The
  verifier recomputes that identity from the already-redacted public command
  summary, so a raw full-command hash is rejected even when it has a valid
  digest shape. Arguments and environment-assignment values therefore have no
  public hash commitment.
- Sanitization uses static compiled redaction patterns plus boundary-aware
  token replacement for repository roots, repository basenames, homes, and
  usernames. It covers quoted assignments, bearer values, representative
  GitHub/GitLab/Slack/OpenAI/Google/npm/PyPI/JWT/AWS token forms, Unix and
  Windows paths, and truncates only at valid UTF-8 boundaries.
- `privacy_test.go` is the maintained adversarial corpus and includes a fuzz
  property for bounded valid UTF-8 output. `contract_depth_test.go` proves
  verifier rejection of a hash that commits to hidden arguments. JSON and
  Markdown continue to render from the same verified typed contract.
- Focused verification: `go test ./internal/proofbundle` passed, including
  deterministic bundle generation, strict decode/verify, privacy corpus, and
  fuzz seed execution.

## Deviations

None.
