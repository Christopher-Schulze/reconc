# reconc v0.10.2

Reconc 0.10.2 is a patch release that strengthens command evidence, staged
candidate verification, safe MCP dispatch, and script process cleanup while
reducing duplicate work in completion gates.

## Fixes

- Staged CI checks require matching index and worktree contents. Unstaged
  corrections or untracked inputs can no longer hide a broken staged candidate.
- Successful command-prefix evidence accepts only statically parsed extensions
  that preserve the required command's success. Shell alternatives, pipelines,
  sequential commands, background execution, and dynamic suffixes cannot turn
  a failing required command into successful evidence.
- `reconc exec` binds command evidence to the session, generation, and write
  epoch captured before execution. Concurrent writes, session switches, resets,
  or removed sessions cannot reassign or freshen completed command evidence.
- Native assurance applies causal freshness at the effective module scope.
  Relevant writes invalidate earlier command evidence without invalidating
  evidence solely because an independent module changed.
- Approval-free MCP calls recover from bounded, pre-dispatch state conflicts
  by reevaluating current policy, identity, windows, and budgets. Retries reuse
  the same call reservation and never repeat a dispatched tool invocation or
  bypass approval requirements.
- Cancelled scripts clean up surviving process-group descendants even after
  the group leader exits. macOS cleanup recognizes native zombie and
  irreversible kernel-exit states while preserving genuine permission errors
  for live process groups.

## Efficiency And Documentation

- One completion attempt reuses its existing policy evaluator for lockfile
  validation and policy evaluation, avoiding duplicate decoding and compilation
  while preserving source freshness and candidate-drift checks.
- Architecture documentation now describes the actual execution-start evidence
  binding, shell success boundaries, and staged candidate requirements.

## Compatibility And Upgrade

No policy-schema or lockfile-format change is introduced, and no manual data
migration is required. Existing immutable schema publication identities remain
unchanged. Staged checks must run against matching staged and worktree inputs;
command evidence that relied on unsafe shell suffixes or stale execution state
is now rejected.

Use the installation's existing owner. For direct installations:

```bash
reconc update
reconc doctor --global
```

After updating the binary, use the documented repository synchronization
workflow to refresh generated integrations in existing repositories.

## Delivery

The tag-bound release workflow verifies immutable schema publication,
LangChain MCP interoperability, source and race-test gates, release trust, and
isolated self-hosting before building the platform artifacts. It verifies the
canonical manifests, checksums, and remote asset inventory before publication.
Published artifacts include build-provenance attestations. Windows artifacts
remain included; optional native Windows smoke is disabled by default.
