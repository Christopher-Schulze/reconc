# reconc v0.8.1

`v0.8.1` is a correctness and portability release: it fixes crash and
data-loss bugs on the bootstrap path, closes silent evaluation gaps around
git path handling and composite freshness, adds opt-in command prefix
matching, verifies installer provenance, and makes the documentation match
the code it describes.

## Evaluation

- Git-derived write paths use NUL-separated (`-z`) output, so filenames
  with unicode or spaces reach policy evaluation verbatim instead of being
  octal-escaped and silently unmatched. Porcelain rename records count the
  origin path as dirty and keep path bytes untrimmed.
- Composite `require_command_success` sub-checks enforce the same
  write-epoch anti-staleness ordering as the top-level kind, and
  `not(require_script)` fails closed when the inner script is missing,
  crashing, or timed out.
- The command kinds accept an additive `command_match: prefix` opt-in with
  token-boundary semantics (`pip install` matches `pip install requests`,
  never `pip installer`); exact matching stays the default. The strict pack
  uses it for its pip rule; agent and release packs list common test flag
  variants; the JSON schemas and RFC-0004 carry the contract.
- Compile warns when a `{identifier}` brace group appears in glob fields of
  kinds without template capture, and when an include fragment resolves
  outside the repository root via symlink.

## Bootstrap And Adoption

- `reconc init` followed by `reconc adopt --apply` produces valid YAML: the
  inline empty `rules: []` scaffold converts to block form, new rules land
  inside the rules block even when other top-level keys follow, rule-id
  deduplication is line-anchored, and the write is atomic.
- Bun repositories get `bun run test` / `bun run lint` suggestions.
- Bootstrap publication falls back to a create-only exclusive copy on
  filesystems without hardlink support.
- The harness scaffold declares its logbook-v1 TASK profile explicitly,
  drops the hardcoded darwin-arm64 onboarding binary, and the workflow-audit
  batch fast path keys on the portable `audits/run-workflow-audit`
  convention instead of a repository-specific path prefix.

## Hooks

- A stray `DEVIN_PROJECT_DIR` environment variable can no longer silently
  no-op non-Devin routes: cross-runtime dedup fires only when the
  first-class platform's hooks are installed in the repository, and it is
  visible on stderr and in liveness.
- Payload reads enforce the documented 5-second stdin deadline; an
  `apply_patch` payload parsing to zero file operations fails closed.
- Claude matchers cover NotebookEdit, TabWrite, StrReplace, and Delete;
  path extraction reads `notebook_path`. Existing installs pick this up by
  re-running `reconc hook install claude-code`.
- The OpenCode/Kilo plugin caps output after reading (Bun.spawn has no
  maxBuffer option) and no longer throws when the host lacks a
  `session.prompt` API. Current Codex permission/stop wire contracts and
  the Kilo plugin API were verified against upstream sources.

## Reliability

- Proof manifests with empty sample sets produce a finding instead of a
  panic; changelog rotation is locked, atomic, deduplicated, and no longer
  stacks archive-pointer trailers; forced hook installs over malformed JSON
  preserve the original bytes in a hash-addressed backup; a run-state close
  error no longer discards a computed Stop decision; `reconc coverage
  check` reads the percentage from real `go test -cover` output; the
  go-concurrency gate accepts struct-field WaitGroups and `go worker(&wg)`
  delegation; unknown TASK dependency ids get a dedicated diagnostic.

## Install And Docs

- `install.sh` verifies the GitHub build-provenance attestation over
  `SHA256SUMS` when `gh` is available; `RECONC_REQUIRE_ATTESTATION=1` makes
  it mandatory.
- The documented command-injection guard is now a real source-scan test; a
  parity test keeps shell completion covering every dispatcher case; the
  doctor audit check measures the real rotation ring; command count, env
  vars, `manpage`, and the package map are documented as they behave.

## Release Artifacts

- `reconc-0.8.1-darwin-amd64`
- `reconc-0.8.1-darwin-arm64`
- `reconc-0.8.1-linux-amd64`
- `reconc-0.8.1-linux-arm64`
- `reconc-0.8.1-windows-amd64.exe`
- deterministic SPDX 2.3 and CycloneDX 1.6 SBOMs
- Bash, Zsh, and Fish completions
- man page
- four public v1 JSON schemas
- `SHA256SUMS`
