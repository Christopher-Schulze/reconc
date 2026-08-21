# TASK 250: Close assurance semantic-validation gaps

## Why

Assurance authoring validation is strict, but several runtime paths weaken the
compiled contract. Package-script gates disappear when an expected manager is
missing, substantive proof reverses the documented zero-age convention, and
runtime applicability and command-policy evaluation can accept malformed state
that the authoring parser would reject. JSON fact readers also disagree on BOM
handling for equivalent package manifests.

## Acceptance

- A gate that declares `package_manager` emits a precise finding when detection
  yields zero managers and an ambiguity finding when it yields multiple; neither
  state silently skips required script evidence.
- `max_age_hours: 0` means no freshness requirement everywhere. Positive values
  enforce the same future-skew and age rules in parser, compiler, runtime, docs,
  and schemas.
- Every `applicable_if` pattern is syntactically validated before any literal or
  glob match can return true. Malformed later patterns cannot hide behind an
  earlier literal match.
- Runtime lockfile loading rejects `command_policy` values other than `all` or
  `any`; evaluator code switches exhaustively and has no implicit fallback.
- UTF-8 BOM policy for package JSON is one explicit contract shared by package
  scripts and dependency pins. Tests cover BOM and non-BOM inputs identically.
- Parser-generated locks, migrated locks, hand-crafted malformed locks, schema
  validation, runtime evaluation, fuzzing, and assurance documentation agree.
- Existing valid assurance packs and deterministic lockfile identities remain
  compatible unless a malformed state is intentionally rejected.

## Sub-Tasks

- [ ] Make absent expected package managers an explicit assurance failure
- [ ] Unify zero freshness semantics for substantive proofs
- [ ] Validate complete applicability pattern sets before matching
- [ ] Reject unknown runtime command policies before evaluation
- [ ] Define and apply one package-JSON BOM contract
- [ ] Add parser/runtime/schema/fuzz regression matrices
- [ ] Run assurance, migration, lockfile, race, and full gates
- [ ] Update assurance authoring and runtime documentation

## Notes

- External findings: F-78, F-79, F-102, F-103, and F-104.
- F-80 is excluded: a deleted or non-regular changed path has no source content
  for content-scanning gates, while symlinks to regular files are currently
  resolved and scanned.

## Deviations

None.
