# TASK 283: Preserve nested shell evaluation semantics

## Why

The shell-command parser reconstructs static `eval` input by joining already
decoded word values with spaces. Quote boundaries and escaped separators are
lost before the nested parse, so a command such as an eval body containing one
quoted argument with spaces can be reinterpreted as multiple arguments. This
can produce incorrect expected/forbidden command matching despite the parser
reporting the input as complete.

## Acceptance

- Static `eval` bodies are reconstructed according to POSIX shell argument
  concatenation semantics, preserving the exact characters passed to `eval`
  after outer quote removal and expansion classification.
- Quoted spaces, empty arguments, escaped quotes/backslashes, semicolons,
  pipelines, redirects, command substitutions, and nested eval/shell wrappers
  retain correct token boundaries through every bounded recursion level.
- Any dynamic expansion, unsupported syntax, ambiguous reconstruction, or depth
  overflow remains incomplete/uncertain and therefore fail closed in deny and
  evidence directions.
- `CompileExpectation`, `Match`, `MatchFoldingExecutable`, runtime forbidden
  command evaluation, and command-evidence matching agree on the corrected
  invocation sequence.
- Table, property, and fuzz tests include Bash/sh reference cases and prove that
  broken reconstruction cannot turn a forbidden invocation into an allow or a
  different invocation into successful evidence.
- Parser complexity remains linear in the bounded command size and recursion
  depth; no shell process is spawned to parse policy or observed commands.
- Command-matching docs, threat model, fuzz corpus, and complete gates pass.

## Sub-Tasks

- [~] Specify the exact static word representation needed for nested eval reconstruction
- [ ] Preserve post-expansion word bytes separately from display rendering
- [ ] Reconstruct and parse eval bodies without lossy whitespace joining
- [ ] Keep every dynamic/ambiguous path explicitly uncertain
- [ ] Add POSIX reference tables, security regressions, properties, and fuzz seeds
- [ ] Verify runtime forbid/evidence consumers and compiled expectations
- [ ] Update command semantics and threat-model documentation
- [ ] Run shellcommand, runtime, fuzz, race, and complete repository gates

## Notes

- Current evidence: `internal/shellcommand/shellcommand.go:nestedCommandString`
  accepts static eval words and returns `wordsSource(words[1:])`; `wordsSource`
  is `strings.Join` over decoded word values and cannot preserve original quote
  grouping.
- This is a semantic correctness task, not a request to execute a real shell or
  import a shell parser dependency. Reuse the existing bounded parser and its
  incomplete-state model.
- Existing fail-closed handling for dynamic words is correct and must remain.

## Deviations

None.
