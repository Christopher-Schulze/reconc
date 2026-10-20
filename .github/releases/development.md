# Development notes

Unversioned development source. Tag creation and release publication require
separate explicit authorization. These notes describe the changes implemented
since the published `reconc-v0.11.0` release and do not establish a published
release.

- One-shot hook processes persist which policy lockfile was validated
  against which source content; a later process that observes the same
  lockfile and source content skips re-reading and re-digesting the policy
  sources. A Claude Code pre-tool hook process took about 8.6 ms instead of
  9.6 ms.
- A policy check inspects each directory of the recorded read evidence once
  instead of every read path twice; on macOS a read listed under its exact
  name in that directory needs no inspection of its own. A pre-tool call in a
  Claude Code session with 1,000 recorded reads took 9.3 ms instead of
  13.8 ms and allocated 3.2 MB instead of 3.9 MB.
