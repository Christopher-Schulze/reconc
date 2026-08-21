# TASK 205: Centralize and enforce template-variable grammar

## Why

Parser and compiler each own an equivalent template-variable regex. Tokens
outside that narrow grammar, such as brace expressions containing a hyphen,
are not recognized as variables and may pass through path validation as
literals. Duplicate grammar owners can also drift between validation,
substitution, compilation, and diagnostics.

## Acceptance

- One package owns the template token grammar, parser, variable extraction,
  validation, substitution, and diagnostic formatting.
- Any unescaped brace expression that looks like a template but is not valid is
  rejected at compile time with a source location; literal-brace escaping is
  documented if supported.
- Supported identifier characters are an explicit compatibility decision, not
  silently broadened by implementation.
- Declared/bound variable relationships and post-substitution repository-path
  validation remain fail closed.
- Differential tests cover valid tokens, repeated variables, malformed braces,
  hyphens, Unicode, escaping, missing bindings, and traversal substitutions.

## Sub-Tasks

- [x] Specify the canonical template grammar and escape rules
- [x] Implement one shared parser and variable representation
- [x] Migrate parser, compiler, and runtime substitution
- [x] Add malformed-token and post-substitution security tests
- [x] Run parser, compiler, runtime, and complete gates

## Notes

- Verified duplicate regex ownership in `internal/parser/parser.go` and
  `internal/compiler/compiler.go`.
- Runtime path substitution already performs containment validation; the old
  session's direct traversal claim was not valid.

## Deviations

None.
