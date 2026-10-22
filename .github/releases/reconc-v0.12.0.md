# reconc v0.12.0

Reconc 0.12.0 reduces hook and Stop overhead, bounds hook waits by the host's
deadline, and keeps MCP action history bounded without weakening replay or
budget enforcement. It also strengthens path identity checks, durability
reporting, and release verification.

## Hook and Stop performance

- One-shot hooks reuse a persisted policy-freshness record only when the
  policy lockfile and source content still match.
- Recorded reads share directory observations and path resolution within a
  request. Bulk observations are confirmed before returning identities, and
  case-only renames, changed directories, and newly created symlinks invalidate
  reuse.
- Stop reuses an index listing only while its content and generation remain
  unchanged, and overlaps Git status with boundary capture.
- Hook wrappers resolve the binary on PATH without a command-substitution
  subshell. Shell analysis and MCP argument and result handling avoid repeated
  parsing within a request.

## Reliability and enforcement

- One-shot hooks, hook workers, and custom runtime bridges cancel lock waits,
  Git calls, and policy evaluation within the host route budget, leaving time
  to return a diagnostic.
- Corrupt saved session reports fail the `session/report-integrity`
  completion check rather than aborting `reconc done` with a decoding error.
- Devin PermissionRequest denials use the host's top-level decision contract.
- PowerShell analysis renders reserved executable names from fixed quoted
  literals and preserves command and argument boundaries.
- Mode-only file repairs synchronize the file before reporting durability;
  platforms that cannot establish it report uncertainty.
- Journaled evidence appends and action-ledger publication use ordered syncs
  within their transactions, retaining the required final durable boundary.
- `doctor --global` includes failed skill checks in its status and exit code,
  reports owned skill drift as stale, and names the targeted repair.

## MCP action state

- The action store issues authenticated, time-bounded call identities and
  prunes completed history once those identities can no longer be admitted.
  Active reservations and budget counters are preserved.
- Admission reserves storage headroom, preventing completed history from
  permanently exhausting the action-state capacity.
- Existing format-1 action state migrates in place to format 2, with recovery
  support for interrupted format-1 transactions.
- Waiting for the MCP transport send slot honors cancellation and deadlines.

## Verification and maintenance

- Host contract monitoring preserves code exactly while normalizing only
  supported presentation differences; regression tests cover escaped
  backticks, code blocks, inline spans, and HTML preformatted text.
- Benchmark RSS gates compare the median of five process peaks and retain
  separate raw peak diagnostics.
- Contributor checks reuse valid Go test results. The release workflow runs
  race shards, Vet, Staticcheck, release trust, and vulnerability checks before
  building artifacts, and requires successful CI and CodeQL for the selected
  commit.
- Release-generator archive extraction uses a bound filesystem root,
  rejects paths and symlinks that escape it, and preserves existing files.
- The legacy hook registry retains compatibility hashes for older generated
  integrations while checking only retained release tags.

## Compatibility and upgrade

No policy-schema or lockfile-format migration is required. Action-state
format migration is automatic. Existing immutable schema identities remain
unchanged, and previously generated managed hooks remain recognizable.

For a direct installation:

```bash
reconc update
reconc doctor --global
```

Use the documented repository synchronization workflow to refresh generated
integrations after updating. Published artifacts retain macOS, Linux, and
Windows builds, checksums, release manifests, and build-provenance
attestations. Native Windows smoke checks remain optional.
