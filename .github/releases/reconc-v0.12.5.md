# reconc v0.12.5

Reconc 0.12.5 refuses an operator claim when Git `HEAD` names no readable
commit, instead of binding it like an unborn branch.

## Operator claims

- `HEAD` counts as unborn only when it is a symbolic reference to a branch
  that does not exist, as before the first commit or on an orphan branch.
- A broken branch reference or a branch that points to a missing commit
  object no longer looks like an unborn branch: recording an operator claim
  fails and no grant is effective until `HEAD` names a readable commit again.
  0.12.3 and 0.12.4 accepted such a claim and bound it to the empty commit.
