# TASK 385: Preserve stricter managed-artifact permissions

## Why

Mode reconciliation applies the exact generated mode to existing hook artifacts. Reinstalling matching content can therefore widen a user-hardened `0600` file to `0644` or a `0700` wrapper to `0755`.

## Acceptance

- Reconciliation never adds read/write/execute permissions beyond those required for the artifact to function.
- Existing stricter modes remain stable when content and required executability are valid.
- Missing owner execute permission is repaired only for executable artifacts.
- Unix and Windows mode-proxy tests cover stricter, insufficient, and unchanged modes.

## Sub-Tasks

- [x] Define required versus preferred permissions for every managed artifact class.
- [x] Reconcile required bits without globally weakening `atomicfile` callers.
- [x] Add adversarial mode regressions for hook install and verify.
- [x] Run focused hooks and atomicfile tests.

## Notes

- Verified from finding 25.
- The change must be scoped to managed-artifact policy; `atomicfile` exact-mode callers may intentionally require exact permissions.
- Source, caller, and Graphify inspection reconfirmed that `writeGeneratedArtifact` forced `0644` or `0755` on every reinstall. Existing `0600` data and `0700` executable artifacts were therefore widened despite matching bytes.
- Generated data and executable artifacts retain `0644` and `0755` as creation defaults. Existing artifacts preserve all read/write/execute bits; only owner execute is added when the generated artifact is executable and lacks it.
- Kimi Code and Codex mixed-ownership configuration paths already preserve their exact existing modes and remain unchanged. `atomicfile` retains exact-mode semantics for every other caller.
- Unix regressions cover matching and content-updating `0600`/`0700` artifacts, unchanged preferred modes, and surgical `0600` to `0700` executable repair through the public Git pre-commit installer and status verifier.
- The Windows mode-proxy regression preserves both read-only and writable states while covering executable intent, whose dispatch contract is not represented by POSIX execute bits on Windows. Windows tests were maintained but not run locally per the requested platform policy.
- Focused gates passed: `go test ./internal/hooks -run 'Test(ManagedArtifactPublicationMode|GeneratedArtifactPublicationPreservesStrictUnixModes|InstallGitPreCommitPreservesStrictMode)' -count=1` and `go test ./internal/hooks ./internal/atomicfile -count=1`.
- Repository fast gate passed: `make test-fast`.
- Full race, release-trust, vet, and lint gates remain reserved for the final queue-wide verification as requested.

## Deviations
