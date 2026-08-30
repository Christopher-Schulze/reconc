# TASK 463: Complete the native Windows final gate

## Why

The first TASK 462 rerun proved the JSONL delete-sharing and Bun-process fixes, but exposed three remaining native Windows defects: fixture files still bypass protected file DACL creation, repository-sync rejection paths retain local-only bound records, and the Grok ACP fake pipe listener can block forever during cleanup.

## Acceptance

- Repository-run fixtures create both directories and files through the production private-filesystem contract.
- Repository-sync rollback and journal-removal rejection paths close local-only bound records without weakening publication recovery records.
- The Windows Grok ACP leader enumeration test owns and terminates its fake pipe listener deterministically.
- Focused tests and Windows cross-compilation pass, and the final GitHub CI plus CodeQL runs are green.

## Sub-Tasks

- [x] Repair repository-run fixture file security.
- [x] Close repository-sync local records on rejected removals.
- [x] Make the Windows Grok ACP fake listener cleanup deterministic.
- [x] Run focused verification, archive, commit, push, and prove all final remote gates.

## Notes

- GitHub Actions run `33324336082`, Windows job `99291708310`, passed JSONL and hooks but failed CLI, retention, and agent-session fixtures with unprotected file DACLs; leaked bootstrap handles during test cleanup; and timed out in `TestWindowsLeaderPipeEnumerationFindsLivePipe` while `go-winio` listener cleanup waited indefinitely.
- Directly materialized private fixture files now use `privatefs.WritePrivateIfChanged`; subsequent adversarial in-place corruption still exercises the original readers and reset paths against protected file identities.
- Repository-sync rollback and transaction-journal removal now close their function-local record after a rejected identity-bound removal. Publication recovery records retain their existing retryable lifetime.
- The enumeration-only fake leader no longer starts an unmatched `Accept`; protocol tests still run the connection-owning goroutine, while enumeration cleanup can close the idle listener immediately.
- Focused bootstrap, CLI, retention, agent-session, and Grok ACP tests pass. All affected packages cross-compile for Windows amd64, and `make vet`, `make lint`, `gofmt`, and `git diff --check` pass. `make test-fast` was not repeated after the immediately preceding green run; the final remote suite is the native Windows execution proof.

## Deviations
