# TASK 247: Secure harness claims and layout portability

## Why

The portable harness currently grants claim authority to an executable whose
freshness is not bound, while several audits contradict the harness's declared
flat-root compatibility or parse source text and module paths too loosely.
These defects can either bypass protected-path enforcement or make valid target
repositories fail hard gates.

## Acceptance

- Task-claim execution verifies the selected repo-local Reconc binary against
  the harness's canonical freshness/provenance identity immediately before
  execution and revalidates identity after opening or execution setup.
- Missing, stale, replaced, symlinked, non-regular, or non-executable claim
  binaries fail closed without asserting any claim.
- Generated-reference audit resolves the configured flat-root or `codebase/`
  generator path through the same stack-layout helper used by other audits.
- Retention uses the same canonical resolved repository identity as runtime
  session state and JSONL pruning, including symlinked invocation paths.
- Placeholder expansion replaces only the documented `{project}` token; a
  literal `project` substring in a user path is preserved.
- Architecture import mapping recognizes configured project module prefixes at
  path boundaries and cannot create internal nodes from coincidental external
  substrings.
- Added-line Go comment scanning is lexical enough to ignore `//` inside quoted
  strings and raw strings while still rejecting prohibited real comments.
- Flat and nested scaffold fixtures, symlink aliases, adversarial binaries,
  import paths, URL strings, deterministic pack generation, and harness race
  tests pass.
- The advanced pack archive and manifest are regenerated only after source and
  tests pass, with deterministic digest verification.

## Sub-Tasks

- [ ] Bind task-claim authority to the canonical binary freshness proof
- [ ] Route generated-reference execution through configured layout resolution
- [ ] Canonicalize retention project keys before every state path is derived
- [ ] Limit project placeholder expansion to the documented token
- [ ] Match architecture module paths only at valid boundaries
- [ ] Replace naive Go line-comment detection with bounded lexical scanning
- [ ] Add flat/nested, alias, hostile-binary, import, and URL regression fixtures
- [ ] Regenerate and verify the deterministic harness pack and documentation

## Notes

- External findings: F-20, F-21, F-32, F-34, F-38, and F-39.
- The claim helper should reuse the existing `auditReconcBinaryFreshness`
  contract rather than create a second provenance scheme.
- No general Go parser dependency is required for one added line; a small
  quote/comment state scanner is sufficient if tests cover interpreted strings,
  raw strings, escapes, rune literals, and real comments.

## Deviations

None.
