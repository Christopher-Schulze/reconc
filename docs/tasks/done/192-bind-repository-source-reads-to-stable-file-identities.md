# TASK 192: Bind repository source reads to stable file identities

## Why

`readRepositorySource` compares resolved path strings before and after a
bounded read. Replacing a regular file at the same lexical path can change its
inode without changing either resolved string. The bounded reader protects its
own open/read window, but the loader's final source-snapshot assertion is not
bound to that opened identity.

## Acceptance

- Repository source loading returns bytes and an identity captured from the
  opened regular file, then proves the current path still names that identity.
- Same-path rename replacement, symlink replacement, deletion/recreation, size
  change, and parent identity change are detected deterministically.
- Containment is checked against the canonical repository root before and after
  the read without following an escaping source.
- Source identity metadata feeds freshness validation without becoming a
  substitute for content hashing.
- Adversarial race tests fail on the current string-only check and pass with the
  stable snapshot implementation.

## Sub-Tasks

- [x] Define repository-source snapshot identity
- [x] Extend bounded regular-file reads to return verified metadata
- [x] Replace string-only source swap detection
- [x] Add deterministic same-path replacement and containment tests
- [x] Run ingest, boundedio, race, and complete gates

## Notes

- Added `boundedio.ReadFileSnapshot`, which returns bounded bytes plus the
  opened file's verified identity while retaining the existing regular-file,
  size, growth, and read-window checks.
- `readRepositorySource` now compares pre/post canonical source and repository
  identities plus opened/post-read `os.FileInfo` identity, mode, size, and
  modification time. Content hashing remains a separate provenance step.
- Snapshot identity and same-size mutation tests pass through `boundedio`;
  existing outside-root symlink tests remain fail-closed.

## Deviations

None.
