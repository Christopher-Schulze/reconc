# TASK 173: Eliminate misleading CodeQL severity signals

## Why

GitHub CodeQL reports 31 Critical or High alerts on the public repository. Seven
integer and allocation findings should be made locally self-evident through
explicit native-width and capacity bounds. The remaining 24 findings trace
untrusted values into diagnostic strings that are serialized as data and never
executed; changing those messages would reduce product fidelity rather than
close a security boundary.

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
- [~] Run candidate CI and CodeQL on the exact commit
- [ ] Reconcile every alert against the new analysis
- [ ] Update public alert state with evidence-backed classifications
- [ ] Reconcile TASK state and publish the completed commit

## Deviations

- A temporary candidate-branch commit is required before TASK completion so
  GitHub CodeQL can analyze the exact source; it will not enter `main` until
  the remote analysis and alert reconciliation pass.
