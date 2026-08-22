# TASK 248: Preserve bootstrap provenance and repository transactions

## Why

Bootstrap, preset, repository-sync, adoption, and command-rendering paths still
contain provenance and identity gaps. Legacy import can mint compatibility
bounds after a pack mismatch, receipt advance removes an existing binary pin,
`adopt --apply` can lose concurrent rules, home and Git metadata identities are
inconsistent, and displayed recovery commands or YAML can be malformed.

## Acceptance

- Legacy plans with a harness-pack digest that cannot be matched to the embedded
  pack fail closed or preserve only bounds proven by authenticated plan data;
  no minimum or maximum version is invented.
- Repository receipt advance preserves the exact `binary@version` component,
  checksum, mode, and ownership for every unchanged approved binary.
- `adopt --apply` executes under the canonical repository transaction lock,
  revalidates the source snapshot before publish, and cannot lose a concurrent
  adopt/init/sync mutation.
- Obsolete private bootstrap receipts have an explicit bounded retention rule.
  Cleanup removes only fully validated Reconc-owned receipts and never current,
  foreign, malformed, or symlinked files.
- User preset and template roots reject symlink directories consistently. Home
  resolution failure is an explicit error, never a CWD-relative fallback.
- Bootstrap and repository sync share one `.git` identity contract covering a
  directory, a valid worktree metadata file, missing metadata, and rejected
  symlinks.
- Suggested shell commands use one platform-appropriate argument renderer that
  preserves quotes, backslashes, dollars, command substitutions, whitespace,
  newlines, and trailing separators as literal argv.
- Adopted YAML uses valid deterministic scalar encoding for every YAML indicator
  prefix and control character; the candidate round-trips through yaml.v3.
- Receipt compatibility, concurrent mutation, symlink, worktree, command argv,
  YAML property, recovery, and end-to-end bootstrap tests pass.

## Sub-Tasks

- [x] Reject or exactly preserve legacy harness-pack provenance on digest mismatch
- [x] Preserve version-pinned binary ownership during receipt advance
- [x] Put adopt read-merge-write under the repository transaction protocol
- [x] Define and implement safe bounded cleanup for obsolete private receipts
- [x] Make home, preset, template, and Git metadata identity fail closed
- [x] Replace bootstrap command quoting with tested literal argv rendering
- [x] Encode adopted YAML scalars through one yaml.v3-compatible path
- [x] Run bootstrap, sync, adopt, preset, race, compatibility, and full gates
- [x] Update bootstrap, worktree, receipt, and recovery documentation

## Notes

- External findings: F-45, F-47, F-48, F-49, F-51, F-52, F-53, F-88, and
  F-89.
- F-50 is excluded because plan-digest recomputation is required to validate an
  externally supplied plan before any mutation.
- Command strings are operator-facing remediation, not a substitute for typed
  internal remediation. Their tests must execute through the exact shell family
  advertised to the user.
- Verification: focused package race suites, `make vet`, `make lint`, the full
  root/template `make test` gate, release-trust fixtures, publication audit, and
  harness-pack integrity all passed. The final retention hardening then passed
  the complete affected-package race suite plus vet and staticcheck again.

## Deviations

None.
