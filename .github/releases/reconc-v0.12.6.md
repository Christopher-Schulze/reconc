# reconc v0.12.6

Reconc 0.12.6 keeps expired operator claims from becoming effective again
after Git reflog cleanup.

## Operator claims

- Grants bind to HEAD and the reflog's size, file identity and change time.
  Rewriting or replacing the reflog expires a grant even when the original
  commit, log contents and modification time return unchanged.
- Confirmed expiry permanently removes the grant under the session lock.
  Stop cannot reuse an earlier allowed decision after the grant expires.
- A temporary Git read failure withholds operator authority without deleting
  an otherwise unexpired grant. Ordinary claims and revocations are preserved.
- Existing grants from earlier storage formats authorize nothing after the
  upgrade. Confirm a fresh operator claim when needed. Reflog maintenance
  also requires a fresh claim, including maintenance that preserves every entry.
