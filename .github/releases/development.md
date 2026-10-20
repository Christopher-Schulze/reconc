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
