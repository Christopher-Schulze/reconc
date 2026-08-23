# TASK 285: Stabilize the Windows MCP lifecycle release gate

## Why

The final `reconc-v0.9.7` commit passed CodeQL and every CI job except one
Windows MCP lifecycle test. The product behavior and the same test passed on
the immediately preceding candidate, while the failing assertion used a fixed
two-second coordination bound below the gateway's canonical lifecycle bound.
Under the full parallel Windows suite, approval startup exceeded that test-only
bound before the handler could publish its deterministic synchronization
signal.

## Acceptance

- The required-ledger fault-injection test retains its exact approval, ledger,
  terminal-state, and no-dispatch assertions.
- Test synchronization uses the existing bounded lifecycle contract instead of
  an unrelated two-second scheduler assumption.
- Focused repeated and race tests pass locally.
- The exact final commit passes CI, including the complete native Windows suite,
  and CodeQL before `reconc-v0.9.7` is tagged or published.

## Sub-Tasks

- [x] Replace the brittle scheduler bound without weakening behavior coverage
- [x] Run focused repeated and race verification
- [x] Commit and push the correction
- [x] Require fresh green CI and CodeQL on the exact final commit
- [x] Archive the verified TASK before release publication

## Notes

- Failed CI run: `32630562251`, exact commit
  `bfab8643d7c22dcd21b88b873d18758c12b2ea0b`.
- CodeQL, macOS, Ubuntu, release trust, and LangChain MCP all passed.
- Windows failed only
  `TestGatewayTerminalizesApprovedReservationWhenRequiredLedgerFails` while
  waiting for the approval handler's start signal.
- The same test passed in Windows CI run `32630023055` on the immediately
  preceding product candidate.
- Local focused verification passed 20 consecutive executions plus the race
  detector with every behavioral assertion intact.
- Candidate `5a524cd4d81df9e82956ce42ba193d4bb46e4e2a` passed CI run
  `32631185099`, including the complete native Windows suite, and CodeQL run
  `32631185096`.
- No release tag or GitHub release exists.

## Deviations
