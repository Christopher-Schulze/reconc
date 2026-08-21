# TASK 201: Eliminate quadratic hook-worker frame assembly

## Why

The former JavaScript worker append path allocated a new array and copied the
entire accumulated prefix for every input chunk. A bounded 128 KiB frame read in
small chunks therefore performs quadratic cumulative copying and creates
avoidable garbage on a latency-sensitive hook path.

## Acceptance

- Frame accumulation uses a bounded growing buffer, chunk list plus one final
  copy, or another representation with linear total copied bytes.
- Maximum frame size, newline framing, UTF-8 decoding, EOF, abort signals, and
  overflow errors remain exactly enforced.
- No promise, event, payload, or oversized intermediate buffer is retained
  after a request settles.
- Tests force one-byte and irregular chunking, exact-limit frames, overflow, and
  cancellation; benchmarks compare allocations and total bytes copied.
- Generated/embedded worker artifacts remain synchronized through the canonical
  source path.

## Sub-Tasks

- [x] Identify the canonical worker source and generation path
- [x] Replace repeated prefix copying with linear accumulation
- [x] Add chunking, limit, cancellation, and lifecycle tests
- [x] Add allocation/copy benchmarks
- [x] Run hook, generation, and complete gates

## Notes

- `TestWorkerResponseBufferContract` exercises the exact
  `appendReconcWorkerBytes` implementation embedded from
  `internal/hooks/worker_client.go`. It covers one-byte and irregular appends,
  the exact 128 KiB limit, non-mutating overflow rejection, buffered line
  remainders, abort, restart, fallback, and clean shutdown.
- `BenchmarkWorkerResponseBufferGeometricGrowth` reports JavaScript execution
  time separately from Bun startup and verifies bounded copied bytes per full
  frame. Generator parity covers OpenCode, Kilo, OMP, and Pi scaffold outputs.
- The session's separate promise-chain memory-leak claim was not proven and is
  not part of this TASK.

## Deviations

None.
