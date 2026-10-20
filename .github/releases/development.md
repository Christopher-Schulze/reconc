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
- A one-shot hook stops waiting for a held session lock or a slow Git call
  when its host's route budget runs out, minus a reporting margin, and returns
  its own diagnostic instead of being killed: a post-tool call behind a held
  session lock ended after 4.5 s with a lock diagnostic instead of waiting
  30 s past the host's 5 s budget.
- A corrupt saved session report fails the `session/report-integrity`
  completion check instead of stopping `reconc done` with an error.
- A case-only rename of a directory or file resolves to its new spelling
  within the same request.
