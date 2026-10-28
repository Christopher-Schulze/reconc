# reconc v0.12.4

Reconc 0.12.4 qualifies the native Codex live probe for codex-cli 0.165.0,
which starts hooks in a new session with an environment rebuilt from its shell
snapshot.

## Native Codex probe

- `reconc hook verify --live --host codex --surface cli --allow-authenticated`
  runs its four allowed and denied write and shell controls with codex-cli
  0.154.0 and 0.165.0.
- A capture whose inherited host process group belongs to another session no
  longer tries the impossible group join, which made every hook of the probe
  fail open under codex-cli 0.165.0; its wrapper keeps its own bounded process
  group instead.
- Every capture records its parent process chain, at most 64 entries, before
  the wrapper runs. The verifier accepts a capture only when it joined the
  owned host process group or, outside any group, the owned Codex process is
  among its ancestors; the host process lives for the whole run, so its
  process ID names no other process meanwhile. Parent lookup uses
  `kern.proc.pid` on macOS and `/proc/<pid>/stat` on Linux.
