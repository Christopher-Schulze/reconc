# Development notes

Unversioned development source. Tag creation and release publication require
separate explicit authorization. These notes describe the changes implemented
since the published `reconc-v0.11.0` release and do not establish a published
release.

## Installation and upgrade

- `reconc update` shows each phase (installation, release, skill, download,
  provenance, smoke test, publish) as it completes. On a terminal the running
  phase animates in place with its elapsed time and the download shows a byte
  progress bar; piped output stays plain. The summary states the version
  change, owner, and skill state, and lists only checks that need attention.
- `reconc doctor --global` no longer emits the unconditional
  `skill-discovery` warning, and update reports no longer repeat the
  pre-update comparison of the skill with the running binary.
