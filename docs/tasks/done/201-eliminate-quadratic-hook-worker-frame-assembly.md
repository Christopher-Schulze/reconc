# TASK 201: Eliminate quadratic hook-worker frame assembly

## Why

The JavaScript worker helper `appendBytes` allocates a new array and copies the
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

- Verified in `appendBytes` and `readWorkerLine` embedded from
  `internal/hooks/worker_client.go`.
- The session's separate promise-chain memory-leak claim was not proven and is
  not part of this TASK.

## Deviations

None.
