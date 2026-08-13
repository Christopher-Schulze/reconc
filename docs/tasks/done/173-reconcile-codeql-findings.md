# TASK 173: Reconcile CodeQL findings

## Why

Fresh CodeQL analysis identified seven integer and allocation hardening
opportunities plus 24 diagnostic-string false positives. The implementation
must make every bound statically provable and classify only source-to-sink paths
that remain serialized data and never reach an execution interpreter.

## Acceptance

- Every integer conversion and combined allocation capacity reported by CodeQL
  has an explicit, testable bound at the conversion or allocation site.
- Boundary tests fail if native-width parsing or 32-bit budget windows regress;
  fresh static analysis proves the allocation sites no longer combine
  attacker-controlled lengths or convert an unbounded Git object size.
- A fresh CodeQL run reports no open High integer or allocation findings on the
  exact candidate commit.
- Every remaining unsafe-quoting alert is revalidated against its complete
  source-to-sink path and closed only as a documented false positive because
  the value remains data and reaches no execution interpreter.
- Core tests, race tests, vet, static analysis, publication, release-trust, and
  protected-main checks pass without changing version `0.9.6`.

## Sub-Tasks

- [x] Add explicit integer and allocation bounds
- [x] Add boundary and regression tests
- [x] Run focused and complete local verification
- [x] Run candidate CI and CodeQL on the exact commit
- [x] Reconcile every alert against the new analysis
- [x] Update public alert state with evidence-backed classifications
- [x] Reconcile TASK state and publish the completed commit

## Deviations

- A temporary candidate branch was used so GitHub CodeQL could analyze the
  exact source before main publication; it was removed after the guarded alert
  reconciliation completed successfully.
- The first completed-main CI pass exposed a scheduler-dependent loss of a
  fatal MCP tool-refresh error during concurrent shutdown. The fatal state is
  now latched and re-reported by both serve and close paths.
