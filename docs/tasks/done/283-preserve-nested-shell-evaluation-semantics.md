# TASK 283: Preserve nested shell evaluation semantics

## Why

An external audit claimed that joining decoded `eval` arguments with spaces
lost quote boundaries. A source and shell-reference review found the opposite:
POSIX `eval` is defined to join the already expanded arguments with one space
and parse that result again. Restoring outer quote boundaries would diverge
from the executed command and could create false allows or false evidence. The
existing behavior therefore needed an explicit contract and adversarial proof,
not a semantic rewrite.

## Acceptance

- Static `eval` bodies follow POSIX shell argument concatenation semantics and
  preserve the exact characters passed to `eval` after outer quote removal and
  expansion classification.
- Quoted spaces, empty arguments, escaped quotes/backslashes, semicolons,
  pipelines, redirects, command substitutions, and nested eval/shell wrappers
  retain correct token boundaries through every bounded recursion level.
- Any dynamic expansion, unsupported syntax, ambiguous reconstruction, or depth
  overflow remains incomplete/uncertain and therefore fail closed in deny and
  evidence directions.
- `CompileExpectation`, `Match`, `MatchFoldingExecutable`, and runtime forbidden
  command evaluation agree on nested invocations. Exact command evidence keeps
  its separate contract and never treats a successful wrapper as proof that a
  directly named command succeeded.
- Table, property, and fuzz tests include Bash/sh reference cases and prove that
  broken reconstruction cannot turn a forbidden invocation into an allow or a
  different invocation into successful evidence.
- Parser complexity remains linear in the bounded command size and recursion
  depth; no shell process is spawned to parse policy or observed commands.
- Command-matching docs, threat model, fuzz corpus, and complete gates pass.

## Sub-Tasks

- [x] Specify the exact static word representation needed for nested eval reconstruction
- [x] Preserve post-expansion word bytes separately from display rendering
- [x] Reconstruct and parse eval bodies with the required single-space concatenation
- [x] Keep every dynamic/ambiguous path explicitly uncertain
- [x] Add POSIX reference tables, security regressions, properties, and fuzz seeds
- [x] Verify runtime forbid/evidence consumers and compiled expectations
- [x] Update command semantics and threat-model documentation
- [x] Run shellcommand, runtime, fuzz, race, and complete repository gates

## Notes

- POSIX specifies that `eval` constructs its command by concatenating arguments
  with one space. GNU Bash 5.3 documents the same behavior. Local Bash 5.3 and
  `/bin/sh` reference probes also agreed on the outer-quote and literal-quote
  cases.
- `commandWord.value` already owns the required post-outer-parse bytes;
  `evalCommandArgument` now names and documents the second-parse boundary.
- This is a semantic correctness task, not a request to execute a real shell or
  import a shell parser dependency. Reuse the existing bounded parser and its
  incomplete-state model.
- Existing fail-closed handling for dynamic words is correct and must remain.

## Deviations

- The proposed preservation of outer quote boundaries was rejected because it
  contradicts POSIX and Bash `eval` semantics. The implementation instead makes
  the existing single-space concatenation contract explicit and locks it down
  with reference, property, fuzz, and runtime-boundary tests.
- Exact require/evidence matching intentionally does not equate `eval X` with a
  direct successful execution of `X`. Nested traversal belongs to the deny
  boundary; treating a wrapper's status as exact direct-command evidence would
  weaken the evidence contract.
