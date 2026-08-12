# TASK 171: Tolerate bounded Action Ledger contention

## Why

Native Windows `main` run `31626729043` showed that four valid Action Ledger
writers can exceed the existing two-second acquisition bound on a heavily
loaded runner. One helper failed closed with `acquire JSONL lock timed out`
even though the holder completed normally and no state was corrupt.

## Acceptance

- Legitimate serialized Action Ledger work has a bounded acquisition window
  large enough for observed native Windows contention.
- A deterministic regression holds the real ledger lock beyond the old limit
  and proves that append succeeds after release.
- Lock acquisition remains context-cancellable and strictly time-bounded.
- Focused tests and complete candidate CI pass on native Windows with source
  version exactly `0.9.6`.

## Sub-Tasks

- [x] Increase the bounded ledger contention window
- [x] Add a real-lock delayed-release regression
- [x] Make the gateway invocation-count fixture publish atomically
- [x] Reconcile release and TASK truth
- [~] Run focused and complete release gates

## Notes

The failed package took substantially longer under concurrent Windows CI load
than the preceding successful candidate runs. The existing two-second bound
was therefore rejecting valid serialized work, not detecting a stuck writer.

The acquisition window is now ten seconds. Call-context cancellation still
wins immediately, and JSONL layout validation continues to reject any timeout
above one minute. The delayed-release regression holds the real ledger lock for
2.5 seconds, beyond the old limit, and passed ten consecutive runs together
with the synchronized multiprocess first-use regression.

The complete measurement run exposed an independent synchronization flaw in its
real child-process fixture: `os.WriteFile` made the count path visible before
writing `1\n`, so a polling reader could observe an empty file. The counter now
serializes increments and atomically publishes complete bytes; assertions stay
exact.

Local release proof is green: `make test`, `make vet`, `make lint`, `make
coverage`, `make fuzz`, `make self-host`, and `make release` passed. Both complete
module coverage profiles passed as review evidence; all 50 root fuzz targets
passed, both modules report no known vulnerabilities, and the pinned real
LangChain stdio integration passed on CPython 3.13.14.

Candidate run `31630010986` correctly rejected an earlier task note that
recorded numeric measurement percentages. The note now records the passed
whole-module profiles without turning review evidence into a policy threshold,
and the exact release-trust script passes locally.

## Deviations

None.
