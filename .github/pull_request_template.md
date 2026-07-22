## Summary

Describe the problem and the bounded change that solves it.

## User-visible behavior

State what changes for users, agents, policy authors, or maintainers. Write
`None` when the change is internal only.

## Evidence

List the exact commands, fixtures, or proof artifacts used to verify the result.

## Review checklist

- [ ] The change is focused and contains no unrelated files.
- [ ] New or changed behavior has failable tests.
- [ ] `make test`, `make vet`, and `make lint` pass, or the exception is explained.
- [ ] `make self-host` passes when bootstrap, hooks, TASKs, or completion behavior changes.
- [ ] `make publication-audit` passes.
- [ ] Public documentation and command surfaces match the implemented behavior.
- [ ] Security, privacy, path, command, and same-user trust boundaries were reviewed.
- [ ] No credentials, private repository material, session data, mutable `.reconc/` state, or build output is included.
- [ ] Release impact is stated; published tags and artifacts remain immutable.
